package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type copilotModel struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Vendor             string   `json:"vendor"`
	ModelPickerEnabled bool     `json:"model_picker_enabled"`
	SupportedEndpoints []string `json:"supported_endpoints"`
	Capabilities       struct {
		Type   string `json:"type"`
		Limits struct {
			MaxContextWindowTokens int `json:"max_context_window_tokens"`
		} `json:"limits"`
		Supports struct {
			Vision bool `json:"vision"`
		} `json:"supports"`
	} `json:"capabilities"`
}

// routeFor says how gopilot will reach a model: the same decision
// handleMessages makes, so the list never advertises a model that 404s.
func routeFor(m copilotModel) string {
	if usesResponsesAPI(m.ID) {
		return "responses"
	}
	if len(m.SupportedEndpoints) == 0 {
		return "chat"
	}
	for _, e := range m.SupportedEndpoints {
		if e == "/chat/completions" {
			return "chat"
		}
	}
	return ""
}

func parseModels(raw []byte) ([]copilotModel, error) {
	var body struct {
		Data []copilotModel `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	return body.Data, nil
}

// printModels lists the chat models Copilot offers in its model picker.
// Models gopilot can't route yet (responses-only, except gpt-5.6-*) are
// shown at the bottom marked "not supported" rather than hidden.
func printModels(w io.Writer, models []copilotModel) {
	var ok, unsupported []copilotModel
	for _, m := range models {
		if m.Capabilities.Type != "chat" || !m.ModelPickerEnabled {
			continue
		}
		if routeFor(m) == "" {
			unsupported = append(unsupported, m)
		} else {
			ok = append(ok, m)
		}
	}
	byID := func(s []copilotModel) {
		sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID })
	}
	byID(ok)
	byID(unsupported)

	fmt.Fprintf(w, "%-26s %-9s %-7s %-10s %s\n", "MODEL", "CONTEXT", "VISION", "VIA", "VENDOR")
	row := func(m copilotModel, via string) {
		ctx := "-"
		if n := m.Capabilities.Limits.MaxContextWindowTokens; n > 0 {
			ctx = fmt.Sprintf("%dk", n/1000)
		}
		vision := "-"
		if m.Capabilities.Supports.Vision {
			vision = "yes"
		}
		fmt.Fprintf(w, "%-26s %-9s %-7s %-10s %s\n", m.ID, ctx, vision, via, m.Vendor)
	}
	for _, m := range ok {
		row(m, routeFor(m))
	}
	if len(unsupported) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "not supported by gopilot yet (Copilot serves them only via /responses):\n")
		ids := make([]string, len(unsupported))
		for i, m := range unsupported {
			ids[i] = m.ID
		}
		fmt.Fprintf(w, "  %s\n", strings.Join(ids, ", "))
	}
}
