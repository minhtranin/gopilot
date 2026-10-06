package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintModels(t *testing.T) {
	raw := []byte(`{"data": [
	  {"id": "gemini-3.8-flash", "vendor": "Google", "model_picker_enabled": true,
	   "supported_endpoints": ["/chat/completions"],
	   "capabilities": {"type": "chat", "limits": {"max_context_window_tokens": 265536}, "supports": {"vision": true}}},
	  {"id": "gpt-5.6-sol", "vendor": "OpenAI", "model_picker_enabled": true,
	   "supported_endpoints": ["/responses"],
	   "capabilities": {"type": "chat", "limits": {"max_context_window_tokens": 400000}, "supports": {"vision": true}}},
	  {"id": "grok-4.7", "vendor": "xAI", "model_picker_enabled": true,
	   "supported_endpoints": ["/responses"],
	   "capabilities": {"type": "chat"}},
	  {"id": "gpt-4o", "model_picker_enabled": false, "capabilities": {"type": "chat"}},
	  {"id": "text-embedding-3-small", "model_picker_enabled": false, "capabilities": {"type": "embeddings"}}
	]}`)
	models, err := parseModels(raw)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	printModels(&buf, models)
	out := buf.String()

	for _, want := range []string{
		"gemini-3.8-flash           265k      yes     chat       Google",
		"gpt-5.6-sol                400k      yes     responses  OpenAI",
		"not supported by gopilot yet",
		"  grok-4.7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"gpt-4o", "text-embedding"} {
		if strings.Contains(out, hidden) {
			t.Errorf("%q should be hidden (not in model picker):\n%s", hidden, out)
		}
	}
}
