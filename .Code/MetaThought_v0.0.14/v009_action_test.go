package main

import (
	"strings"
	"testing"
)

func v9ActionReply(command, body, scores string) string {
	return "# ACTION\n" + command + "\n# YOUR ANSWER\n" + body + "\n# YOUR SCORE\n" + scores
}

func TestV009InquiryActionParsesOperationsAndIndependentScores(t *testing.T) {
	for _, test := range []struct {
		command string
		kind    string
		source  int64
		target  int64
	}{
		{"PROPOSE", "PROPOSE", 0, 0},
		{"JOIN 7", "JOIN", 7, 0},
		{"CONTRIBUTE 7", "CONTRIBUTE", 7, 0},
		{"CONNECT 7 12", "CONNECT", 7, 12},
		{"PASS", "PASS", 0, 0},
	} {
		t.Run(test.command, func(t *testing.T) {
			body := "Could the competing explanations predict different observations?\nA distinction could change which experiment matters."
			got, err := ParseInquiryAction(v9ActionReply(test.command, body, "relevance: 1\ninquiry_value: 1"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != test.kind || got.InquiryID != test.source || got.TargetID != test.target || got.Body != body {
				t.Fatalf("operation or body changed: %#v", got)
			}
			if got.Relevance != 1 || got.InquiryValue != 1 {
				t.Fatalf("independent scores were changed: %#v", got)
			}
		})
	}
}

func TestV009InquiryActionRetainsBodyAndExplicitZero(t *testing.T) {
	text := "\r\n# ACTION\r\nCONNECT\t3\t9\r\n\r\n# YOUR ANSWER\r\nFirst observation.\r\n  Retain this indentation: uncertain.\r\n\r\n# YOUR SCORE\r\ninquiry_value: 5e-1\r\nrelevance: 0\r\n"
	got, err := ParseInquiryAction(text)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "First observation.\r\n  Retain this indentation: uncertain." {
		t.Fatalf("body's internal text changed: %q", got.Body)
	}
	if got.Relevance != 0 || got.InquiryValue != 0.5 || got.InquiryID != 3 || got.TargetID != 9 {
		t.Fatalf("explicit scores or IDs changed: %#v", got)
	}
}

func TestV009InquiryActionRejectsIncompleteOrAmbiguousProtocol(t *testing.T) {
	valid := v9ActionReply("JOIN 2", "I can challenge the proposed explanation.", "relevance: 0.7\ninquiry_value: 0.4")
	cases := map[string]string{
		"empty":             "",
		"preamble":          "Here is my action.\n" + valid,
		"unknown heading":   valid + "\n# CONFIDENCE\n0.9",
		"nested heading":    strings.Replace(valid, "I can challenge", "## Details\nI can challenge", 1),
		"duplicate heading": valid + "\n# YOUR ANSWER\nAnother answer.",
		"wrong heading":     strings.Replace(valid, "# YOUR SCORE", "# Your Score", 1),
		"missing action":    strings.Replace(valid, "# ACTION\nJOIN 2\n", "", 1),
		"missing answer":    strings.Replace(valid, "# YOUR ANSWER\nI can challenge the proposed explanation.\n", "", 1),
		"missing scores":    strings.Split(valid, "# YOUR SCORE")[0],
		"empty answer":      v9ActionReply("PASS", " \n\t", "relevance: 0\ninquiry_value: 0"),
		"multiple commands": v9ActionReply("JOIN 2\nCONTRIBUTE 2", "Two operations.", "relevance: 0.7\ninquiry_value: 0.4"),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseInquiryAction(text); err == nil {
				t.Fatalf("accepted ambiguous or incomplete reply: %#v", got)
			}
		})
	}
}

func TestV009InquiryActionRejectsBadCommandsAndIDs(t *testing.T) {
	for _, command := range []string{
		"", "LEAD 2", "join 2", "PROPOSE 2", "PASS 2", "JOIN", "JOIN 2 3",
		"CONTRIBUTE", "CONTRIBUTE 2 3", "CONNECT 2", "CONNECT 2 3 4", "JOIN\n2",
		"JOIN 0", "JOIN -1", "JOIN +1", "JOIN 1.5", "JOIN 1e2", "JOIN 9223372036854775808", "CONNECT 2 0",
	} {
		t.Run(command, func(t *testing.T) {
			if got, err := ParseInquiryAction(v9ActionReply(command, "Reason for action.", "relevance: 0.7\ninquiry_value: 0.4")); err == nil {
				t.Fatalf("accepted invalid command: %#v", got)
			}
		})
	}
}

func TestV009InquiryActionRequiresBothActualFiniteUnitScores(t *testing.T) {
	for _, scores := range []string{
		"", "relevance: 0", "inquiry_value: 0", "relevance: 0.5\ninquiry_value:",
		"relevance: 0.5\ninquiry_value: 0.4\nrelevance: 0.8",
		"relevance: 0.5\ninquiry_value: 0.4\nconfidence: 0.9",
		"relevance: 0.5\ninquiry_value: 0.4\nThis looks useful.",
		"relevance = 0.5\ninquiry_value: 0.4",
	} {
		t.Run(scores, func(t *testing.T) {
			if got, err := ParseInquiryAction(v9ActionReply("PASS", "No useful change yet.", scores)); err == nil {
				t.Fatalf("accepted missing, duplicate or unrecognized score: %#v", got)
			}
		})
	}
	for _, name := range []string{"relevance", "inquiry_value"} {
		for _, value := range []string{"NaN", "Inf", "+Inf", "-Inf", "-0.01", "1.01", "1e309", "0.5 because plausible", "0.5 0.6", "unknown"} {
			t.Run(name+"_"+value, func(t *testing.T) {
				scores := "relevance: 0.4\ninquiry_value: 0.4"
				scores = strings.Replace(scores, name+": 0.4", name+": "+value, 1)
				if got, err := ParseInquiryAction(v9ActionReply("PROPOSE", "A question and why it matters.", scores)); err == nil {
					t.Fatalf("accepted invalid score: %#v", got)
				}
			})
		}
	}
}
