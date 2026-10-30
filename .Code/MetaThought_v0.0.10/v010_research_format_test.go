package main

import (
	"strings"
	"testing"
)

func TestV010ResearchFormatPreservesNestedAnswerHeadings(t *testing.T) {
	body := "## Findings: prior sources found\nOriginal evidence α.\n### Comparison\nMechanism differs.\n#### Reason\nA plain explanation.\n##### Evidence\n[Original source](https://source.invalid/paper).\n###### Uncertainty\nSearch scope remains limited.\n## YOUR SCORE\nThis is an answer subheading, not the score section.\n## Findings\nRepeated answer subheadings remain ordinary content."
	for _, command := range []string{"RESEARCH 3", "CONTRIBUTE 3"} {
		t.Run(command, func(t *testing.T) {
			reply := "# ACTION\n" + command + "\n\n# YOUR ANSWER\n" + body + "\n\n# YOUR SCORE\nrelevance: 0\ninquiry_value: 0.75"
			got, err := ParseInquiryAction(reply)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != strings.Fields(command)[0] || got.InquiryID != 3 || got.Body != body || got.Relevance != 0 || got.InquiryValue != 0.75 {
				t.Fatalf("nested answer content or exact scores changed: %#v", got)
			}
		})
	}
	// Monitoring uses the same envelope parser; its distinct vocabulary stays intact.
	queen, err := ParseQueenWorkerAction("# ACTION\nGUIDE\n# YOUR ANSWER\n" + body + "\n# YOUR SCORE\nrelevance: 1\ninquiry_value: 0.5")
	if err != nil || queen.Kind != "GUIDE" || queen.Body != body {
		t.Fatalf("shared answer-envelope behavior changed: %#v, %v", queen, err)
	}
}

func TestV010ResearchFormatKeepsControlSectionsAndScoresStrict(t *testing.T) {
	valid := "# ACTION\nRESEARCH 3\n# YOUR ANSWER\n## Findings\nOriginal answer.\n# YOUR SCORE\nrelevance: 0.5\ninquiry_value: 0.5"
	cases := map[string]string{
		"unknown H1 in answer":  strings.Replace(valid, "## Findings", "# Findings", 1),
		"duplicate H1 answer":   strings.Replace(valid, "Original answer.", "Original answer.\n# YOUR ANSWER\nAnother answer.", 1),
		"duplicate H1 action":   valid + "\n# ACTION\nRESEARCH 3",
		"duplicate H1 score":    valid + "\n# YOUR SCORE\nrelevance: 0.5\ninquiry_value: 0.5",
		"unknown H1 at end":     valid + "\n# SOURCES\nhttps://source.invalid",
		"H7 answer heading":     strings.Replace(valid, "## Findings", "####### Findings", 1),
		"missing H1 score":      strings.Replace(valid, "# YOUR SCORE", "## YOUR SCORE", 1),
		"decorated command":     strings.Replace(valid, "RESEARCH 3", "RESEARCH 3 — prior-art search", 1),
		"command second line":   strings.Replace(valid, "RESEARCH 3", "RESEARCH 3\nInvestigate the mechanism.", 1),
		"missing relevance":     strings.Replace(valid, "relevance: 0.5\n", "", 1),
		"missing inquiry value": strings.Replace(valid, "inquiry_value: 0.5", "", 1),
		"duplicate relevance":   valid + "\nrelevance: 0.5",
		"score explanation":     valid + " because it advances the inquiry",
	}
	for level := 2; level <= 6; level++ {
		heading := strings.Repeat("#", level) + " Nested"
		cases[heading+" in ACTION"] = strings.Replace(valid, "RESEARCH 3", "RESEARCH 3\n"+heading, 1)
		cases[heading+" in YOUR SCORE"] = valid + "\n" + heading
	}
	for _, name := range []string{"relevance", "inquiry_value"} {
		for _, value := range []string{"NaN", "Inf", "-Inf", "-0.1", "1.1", "unknown", ""} {
			cases[name+"="+value] = strings.Replace(valid, name+": 0.5", name+": "+value, 1)
		}
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseInquiryAction(reply); err == nil {
				t.Fatalf("accepted invalid control structure or score: %#v", got)
			}
		})
	}
}
