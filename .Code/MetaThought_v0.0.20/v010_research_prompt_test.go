package main

import (
	"strings"
	"testing"
)

// The first live research run exposed a stale context instruction overriding
// the monitor's new capability statement. Exercise the actual assembled Queen
// prompt, not just the newly added research prompt in isolation.
func TestV010ResearchCapabilitiesAgreeAcrossActivePrompts(t *testing.T) {
	c, err := LoadConfig("config.json")
	if err != nil {
		t.Fatal(err)
	}
	h := &Hive{Config: c}
	for label, prompt := range map[string]string{
		"queen monitor": h.workerQueenPrompt(),
		"queen return": c.Prompts["worker_queen_return"],
		"drone actions": c.Prompts["inquiry_actions"],
	} {
		lower := strings.ToLower(prompt)
		if !strings.Contains(prompt, "RESEARCH") || strings.Contains(lower, "web search and trials are unavailable") || strings.Contains(lower, "no web-search or trial tool") || strings.Contains(lower, "no web-search tool") {
			t.Fatalf("%s contradicts the available research action", label)
		}
	}
	if !strings.Contains(c.Prompts["worker_research"], "web_search") || !strings.Contains(c.Prompts["worker_team_assess"], "Score: INCOMPLETE") {
		t.Fatal("research execution or missing-evidence outcome is not configured")
	}
}
