package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

type quotaSnapshot struct {
	Entitlement      float64 `json:"entitlement"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percent_remaining"`
	Unlimited        bool    `json:"unlimited"`
	OverageCount     float64 `json:"overage_count"`
	OveragePermitted bool    `json:"overage_permitted"`
}

type copilotUser struct {
	Login          string                   `json:"login"`
	Plan           string                   `json:"copilot_plan"`
	QuotaResetDate string                   `json:"quota_reset_date"`
	Quotas         map[string]quotaSnapshot `json:"quota_snapshots"`
}

func fetchCopilotUser(githubToken string) (copilotUser, error) {
	var out copilotUser
	req, _ := http.NewRequest("GET", "https://api.github.com/copilot_internal/user", nil)
	for k, v := range githubHeaders(githubToken) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return out, fmt.Errorf("copilot usage: http %d: %s", resp.StatusCode, b)
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func printUsage(w io.Writer, u copilotUser) {
	fmt.Fprintf(w, "account : %s\n", u.Login)
	fmt.Fprintf(w, "plan    : %s\n", u.Plan)
	if u.QuotaResetDate != "" {
		fmt.Fprintf(w, "resets  : %s\n", u.QuotaResetDate)
	}
	if len(u.Quotas) == 0 {
		return
	}
	fmt.Fprintln(w)

	// premium_interactions is the one that actually runs out; show it first.
	names := make([]string, 0, len(u.Quotas))
	for n := range u.Quotas {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == "premium_interactions") != (names[j] == "premium_interactions") {
			return names[i] == "premium_interactions"
		}
		return names[i] < names[j]
	})

	for _, n := range names {
		q := u.Quotas[n]
		if q.Unlimited {
			fmt.Fprintf(w, "%-22s unlimited\n", n)
			continue
		}
		used := q.Entitlement - q.Remaining
		line := fmt.Sprintf("%-22s %5.1f%% used  (%.0f / %.0f, %.0f left)",
			n, 100-q.PercentRemaining, used, q.Entitlement, q.Remaining)
		if q.OverageCount > 0 {
			line += fmt.Sprintf("  overage %.0f", q.OverageCount)
		}
		fmt.Fprintln(w, line)
	}
}
