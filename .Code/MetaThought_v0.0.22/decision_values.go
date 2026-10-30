package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Normalize the ordered wire values only for the existing typed validator.
// The provider's original reply remains unchanged in diagnostics.
func decisionSectionsFromValues(q DecisionQuestion, reply string) (string, error) {
	var values []string
	for _, line := range strings.Split(reply, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			values = append(values, value)
		}
	}
	if q.Type == "predicate" {
		if len(values) != 1 {
			return "", fmt.Errorf("predicate requires one ordered probability")
		}
		return "# PROBABILITY\n" + values[0], nil
	}
	size, offset := len(q.Levels), 0
	if q.Type == "choice" {
		size, offset = len(q.Choices), 1
	}
	if q.Type != "choice" && q.Type != "score" {
		return "", fmt.Errorf("unsupported ordered decision type")
	}
	if len(values) != size+offset+1 {
		return "", fmt.Errorf("decision requires %d ordered values", size+offset+1)
	}
	var out strings.Builder
	if q.Type == "choice" {
		out.WriteString("# CHOICE\n" + values[0] + "\n")
	}
	out.WriteString("# PROBABILITIES\n")
	for i := 0; i < size; i++ {
		out.WriteString(strconv.Itoa(i+1) + ": " + values[i+offset] + "\n")
	}
	out.WriteString("# CONFIDENCE\n" + values[len(values)-1])
	return out.String(), nil
}
