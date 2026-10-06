package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrintUsage(t *testing.T) {
	raw := `{
	  "login": "someone",
	  "copilot_plan": "business",
	  "quota_reset_date": "2026-11-01",
	  "quota_snapshots": {
	    "chat": {"unlimited": true, "percent_remaining": 100},
	    "completions": {"unlimited": true, "percent_remaining": 100},
	    "premium_interactions": {"entitlement": 300, "remaining": 175.8, "percent_remaining": 58.6, "overage_count": 0}
	  }
	}`
	var u copilotUser
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	printUsage(&buf, u)
	out := buf.String()

	for _, want := range []string{
		"account : someone",
		"plan    : business",
		"resets  : 2026-11-01",
		"premium_interactions    41.4% used  (124 / 300, 176 left)",
		"chat                   unlimited",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "premium_interactions") > strings.Index(out, "chat") {
		t.Errorf("premium_interactions should be listed first:\n%s", out)
	}
}
