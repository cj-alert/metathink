package main

import (
	"strings"
	"testing"
)

func TestV010MarkdownControlPresentationPreservesActionAndAnswer(t *testing.T) {
	body := "## Finding\nOriginal **bold evidence** and `literal code`.\n### Uncertainty\nKeep this body unchanged: α.\n## YOUR SCORE\nThis is an answer subheading."
	commands := []string{
		"## RESEARCH 8", // Exact command presentation that stopped the live hive.
		"###### RESEARCH 8",
		"`RESEARCH 8`",
		"**RESEARCH 8**",
		"__RESEARCH 8__",
		"**RESEARCH** `8`",
		"- **RESEARCH 8**",
		"### **RESEARCH 8**",
	}
	scores := []string{
		"relevance: 0\ninquiry_value: 0.75",
		"* **relevance:** `0`\n- `inquiry_value`: **0.75**",
		"- **relevance: 0**\n+ __inquiry_value: 0.75__",
		"## relevance: 0\n### inquiry_value: 0.75",
	}
	for _, command := range commands {
		for _, score := range scores {
			t.Run(command+"/"+score, func(t *testing.T) {
				reply := "# ACTION\n" + command + "\n# YOUR ANSWER\n" + body + "\n# YOUR SCORE\n" + score
				got, err := ParseInquiryAction(reply)
				if err != nil || got.Kind != "RESEARCH" || got.InquiryID != 8 || got.TargetID != 0 || got.Relevance != 0 || got.InquiryValue != 0.75 || got.Body != body {
					t.Fatalf("presentation changed command, scores or original answer: %+v error=%v", got, err)
				}
			})
		}
	}
	queen, err := ParseQueenWorkerAction("# ACTION\n## **GUIDE**\n# YOUR ANSWER\n" + body + "\n# YOUR SCORE\n- relevance: `0.8`\n- inquiry_value: **0.7**")
	if err != nil || queen.Kind != "GUIDE" || queen.Body != body || queen.Relevance != 0.8 || queen.InquiryValue != 0.7 {
		t.Fatalf("shared Queen parser lost valid presentation: %+v error=%v", queen, err)
	}
	connection, err := ParseInquiryAction("# ACTION\n**CONNECT** `8` **3**\n# YOUR ANSWER\nOriginal offered idea.\n# YOUR SCORE\nrelevance: 1\ninquiry_value: 0.5")
	if err != nil || connection.Kind != "CONNECT" || connection.InquiryID != 8 || connection.TargetID != 3 {
		t.Fatalf("wrapped command tokens changed IDs: %+v error=%v", connection, err)
	}
}

func TestV010MarkdownControlPresentationRetainsSemanticValidation(t *testing.T) {
	valid := "# ACTION\n## RESEARCH 8\n# YOUR ANSWER\n## Findings\nOriginal answer.\n# YOUR SCORE\n- **relevance:** `0.5`\n- **inquiry_value:** `0.75`"
	cases := map[string]string{
		"extra command prose":                strings.Replace(valid, "## RESEARCH 8", "## RESEARCH 8 — search the literature", 1),
		"extra command":                      strings.Replace(valid, "## RESEARCH 8", "## RESEARCH 8\n**PASS**", 1),
		"missing ID":                         strings.Replace(valid, "## RESEARCH 8", "`RESEARCH`", 1),
		"extra ID":                           strings.Replace(valid, "## RESEARCH 8", "**RESEARCH 8 9**", 1),
		"zero ID":                            strings.Replace(valid, "## RESEARCH 8", "**RESEARCH** `0`", 1),
		"negative ID":                        strings.Replace(valid, "## RESEARCH 8", "**RESEARCH** `-8`", 1),
		"unknown action":                     strings.Replace(valid, "## RESEARCH 8", "## INVESTIGATE 8", 1),
		"unclosed wrapper":                   strings.Replace(valid, "## RESEARCH 8", "`RESEARCH 8", 1),
		"H1 command":                         strings.Replace(valid, "## RESEARCH 8", "# RESEARCH 8", 1),
		"H7 command":                         strings.Replace(valid, "## RESEARCH 8", "####### RESEARCH 8", 1),
		"unknown H1":                         strings.Replace(valid, "## Findings", "# Findings", 1),
		"duplicate H1":                       valid + "\n# ACTION\nPASS",
		"missing exact score section":        strings.Replace(valid, "# YOUR SCORE", "## YOUR SCORE", 1),
		"duplicate wrapped score":            valid + "\n**relevance**: **0.5**",
		"unknown score":                      valid + "\n- **confidence:** `0.5`",
		"score prose":                        strings.Replace(valid, "`0.75`", "`0.75` because useful", 1),
		"missing score":                      strings.Replace(valid, "- **inquiry_value:** `0.75`", "", 1),
		"minus is not a bullet inside value": strings.Replace(valid, "`0.75`", "- 0.75", 1),
	}
	for _, value := range []string{"NaN", "Inf", "-Inf", "-0.1", "1.1", "unknown", "1e309", ""} {
		cases["wrapped invalid score="+value] = strings.Replace(valid, "`0.75`", "**"+value+"**", 1)
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseInquiryAction(reply); err == nil {
				t.Fatalf("presentation normalization accepted invalid semantics: %+v", got)
			}
		})
	}
}
