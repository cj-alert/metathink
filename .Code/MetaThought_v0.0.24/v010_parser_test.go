package main

import (
	"strings"
	"testing"
)

func v10ParserReply(command, answer, scores string) string {
	return "# ACTION\n" + command + "\n# YOUR ANSWER\n" + answer + "\n# YOUR SCORE\n" + scores
}

func TestV010DroneActionsIncludeDiscoveryReadAndSpawn(t *testing.T) {
	for _, test := range []struct {
		command string
		kind    string
		id      int64
		target  int64
	}{
		{"PROPOSE", "PROPOSE", 0, 0},
		{"JOIN 2", "JOIN", 2, 0},
		{"CONTRIBUTE 3", "CONTRIBUTE", 3, 0},
		{"CONNECT 3 4", "CONNECT", 3, 4},
		{"PASS", "PASS", 0, 0},
		{"SPAWN 7", "SPAWN", 7, 0},
		{"DISCOVER", "DISCOVER", 0, 0},
		{"READ 9223372036854775807", "READ", 9223372036854775807, 0},
	} {
		t.Run(test.command, func(t *testing.T) {
			body := "This is the proposed action's basis.\nThe evidence remains incomplete."
			got, err := ParseInquiryAction(v10ParserReply(test.command, body, "relevance: 1\ninquiry_value: 1"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != test.kind || got.InquiryID != test.id || got.TargetID != test.target || got.Body != body || got.Relevance != 1 || got.InquiryValue != 1 {
				t.Fatalf("action or independent scores changed: %#v", got)
			}
		})
	}
}

func TestV010NewDroneActionsRejectBadArityAndIDs(t *testing.T) {
	for _, command := range []string{
		"SPAWN", "SPAWN 1 2", "SPAWN 0", "SPAWN -1", "DISCOVER 1",
		"READ", "READ 1 2", "READ 0", "READ +1", "READ 1.5", "READ 1e2",
		"READ 9223372036854775808", "READ\n1", "SPAWN 1\nJOIN 1", "spawn 1",
	} {
		t.Run(command, func(t *testing.T) {
			if got, err := ParseInquiryAction(v10ParserReply(command, "Reason and uncertainty.", "relevance: 0.5\ninquiry_value: 0.5")); err == nil {
				t.Fatalf("accepted invalid action: %#v", got)
			}
		})
	}
}

func TestV010QueenMonitoringActionsPreserveLocalScores(t *testing.T) {
	for _, command := range []string{"NOOP", "GUIDE", "BEGIN_COLLAPSE"} {
		t.Run(command, func(t *testing.T) {
			body := "The submitted evidence supports this monitoring decision.\r\n  A material disagreement remains unresolved."
			got, err := ParseQueenWorkerAction(v10ParserReply(command, body, "relevance: 0\ninquiry_value: 0.75"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != command || got.Body != body || got.Relevance != 0 || got.InquiryValue != 0.75 {
				t.Fatalf("monitoring decision or explicit zero changed: %#v", got)
			}
		})
	}
}

func TestV010WorkerActionVocabulariesStayDistinct(t *testing.T) {
	for _, command := range []string{"NOOP", "GUIDE", "BEGIN_COLLAPSE"} {
		if _, err := ParseInquiryAction(v10ParserReply(command, "Reason.", "relevance: 0.5\ninquiry_value: 0.5")); err == nil {
			t.Fatalf("drone parser accepted Queen action %q", command)
		}
	}
	for _, command := range []string{"PROPOSE", "JOIN 1", "CONTRIBUTE 1", "CONNECT 1 2", "PASS", "SPAWN 1", "DISCOVER", "READ 1", "RETURN", "GUIDE 1", "NOOP 1", "BEGIN_COLLAPSE 1", "GUIDE\nNOOP"} {
		if _, err := ParseQueenWorkerAction(v10ParserReply(command, "Reason.", "relevance: 0.5\ninquiry_value: 0.5")); err == nil {
			t.Fatalf("Queen parser accepted non-monitoring command %q", command)
		}
	}
}

func TestV010BothParsersRequireCompleteActualLocalScores(t *testing.T) {
	parsers := map[string]struct {
		command string
		parse   func(string) error
	}{
		"drone": {"DISCOVER", func(text string) error { _, err := ParseInquiryAction(text); return err }},
		"queen": {"GUIDE", func(text string) error { _, err := ParseQueenWorkerAction(text); return err }},
	}
	for actor, parser := range parsers {
		t.Run(actor, func(t *testing.T) {
			valid := v10ParserReply(parser.command, "Decision basis and uncertainty.", "relevance: 0.5\ninquiry_value: 0.5")
			cases := map[string]string{
				"missing heading":    strings.Replace(valid, "# YOUR SCORE\nrelevance: 0.5\ninquiry_value: 0.5", "", 1),
				"duplicate heading":  valid + "\n# YOUR ANSWER\nAnother answer.",
				"unknown heading":    valid + "\n# CONFIDENCE\n0.5",
				"missing relevance":  strings.Replace(valid, "relevance: 0.5\n", "", 1),
				"missing value":      strings.Replace(valid, "inquiry_value: 0.5", "", 1),
				"duplicate score":    valid + "\ninquiry_value: 0.5",
				"unknown score":      valid + "\nnovelty: 1",
				"empty answer":       strings.Replace(valid, "Decision basis and uncertainty.", " \n\t", 1),
				"score commentary":   valid + " because this seems useful",
				"extra introduction": "Here is the answer.\n" + valid,
			}
			for name, text := range cases {
				t.Run(name, func(t *testing.T) {
					if err := parser.parse(text); err == nil {
						t.Fatal("accepted incomplete or ambiguous local assessment")
					}
				})
			}
			for _, name := range []string{"relevance", "inquiry_value"} {
				for _, value := range []string{"NaN", "Inf", "-Inf", "-0.1", "1.1", "1e309", "unknown"} {
					t.Run(name+"_"+value, func(t *testing.T) {
						text := strings.Replace(valid, name+": 0.5", name+": "+value, 1)
						if err := parser.parse(text); err == nil {
							t.Fatal("accepted a nonfinite, out-of-range or nonnumeric score")
						}
					})
				}
			}
		})
	}
}
