package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// InquiryAction is one proposed operation and its local scores. The scores do
// not assess a parent hypothesis, and neither score is a missing-value default.
type InquiryAction struct {
	Kind         string  `json:"kind"`
	InquiryID    int64   `json:"inquiry_id,omitempty"`
	TargetID     int64   `json:"target_id,omitempty"`
	Body         string  `json:"body"`
	Relevance    float64 `json:"relevance"`
	InquiryValue float64 `json:"inquiry_value"`
}

// ParseInquiryAction parses plain metadata lines or legacy text sections. Inquiry existence,
// membership and the meaning of the answer belong to the runner and its evidence;
// parsing cannot establish the quality or novelty of an otherwise valid action.
// READ uses InquiryID for a work artifact ID. SPAWN requests an independent
// participant invitation; it never represents a commitment by that participant.
func ParseInquiryAction(text string) (InquiryAction, error) {
	return parseScoredWorkerAction(text, map[string]int{
		"PROPOSE": 0, "JOIN": 1, "CONTRIBUTE": 1, "CONNECT": 2,
		"PASS": 0, "SPAWN": 1, "DISCOVER": 0, "READ": 1, "RESEARCH": 1,
	})
}

// Both worker kinds use the same text formats and mandatory local score
// fields. Their allowed operations remain distinct.
func parseScoredWorkerAction(text string, arities map[string]int) (InquiryAction, error) {
	sections := map[string][]string{}
	lines := strings.Split(text, "\n")
	actionLine, plainCommand, plain := workerPlainActionStart(lines, arities)
	scoreStart := len(lines)
	if plain {
		sections["YOUR ANSWER"] = []string{}
		start, values, err := workerPlainScoreTail(lines, actionLine)
		if err != nil {
			return InquiryAction{}, err
		}
		scoreStart = start
		sections["YOUR SCORE"] = []string{"relevance:" + values[0], "inquiry_value:" + values[1]}
	}
	current := ""
	for index, original := range lines {
		if plain {
			if index == actionLine {
				sections["ACTION"] = []string{plainCommand}
				continue
			}
			if index >= scoreStart {
				continue
			}
			if name, _, found := strings.Cut(workerControlText(original), ":"); found && strings.EqualFold(workerUnwrapControlText(name), "ACTION") {
				return InquiryAction{}, fmt.Errorf("action line %d: duplicate ACTION field", index+1)
			}
			sections["YOUR ANSWER"] = append(sections["YOUR ANSWER"], original)
			continue
		}
		line := strings.TrimSpace(original)
		if strings.HasPrefix(line, "#") {
			// H2-H6 remain original answer content. In control sections they
			// may present a command or score; their content is still validated.
			rest := strings.TrimLeft(line, "#")
			level := len(line) - len(rest)
			if current != "" && level >= 2 && level <= 6 &&
				(rest == "" || rest[0] == ' ' || rest[0] == '\t') {
				sections[current] = append(sections[current], original)
				continue
			}
			var name string
			switch line {
			case "# ACTION":
				name = "ACTION"
			case "# YOUR ANSWER":
				name = "YOUR ANSWER"
			case "# YOUR SCORE":
				name = "YOUR SCORE"
			default:
				return InquiryAction{}, fmt.Errorf("action line %d: unknown heading %q", index+1, line)
			}
			if _, exists := sections[name]; exists {
				return InquiryAction{}, fmt.Errorf("action line %d: duplicate heading %q", index+1, name)
			}
			sections[name], current = []string{}, name
			continue
		}
		if current == "" {
			if line != "" {
				return InquiryAction{}, fmt.Errorf("action line %d: expected a section heading", index+1)
			}
			continue
		}
		sections[current] = append(sections[current], original)
	}
	for _, name := range []string{"ACTION", "YOUR ANSWER", "YOUR SCORE"} {
		if _, exists := sections[name]; !exists {
			return InquiryAction{}, fmt.Errorf("action missing heading %q", name)
		}
	}

	command := ""
	for _, line := range sections["ACTION"] {
		line = workerControlText(line)
		if line == "" {
			continue
		}
		if command != "" {
			return InquiryAction{}, fmt.Errorf("ACTION must contain exactly one command line")
		}
		command = line
	}
	parts := strings.Fields(command)
	for index := range parts {
		parts[index] = workerUnwrapControlText(parts[index])
	}
	if len(parts) == 0 {
		return InquiryAction{}, fmt.Errorf("ACTION requires a command")
	}
	result := InquiryAction{Kind: parts[0]}
	arguments, known := arities[result.Kind]
	if !known {
		return InquiryAction{}, fmt.Errorf("unknown action %q", result.Kind)
	}
	if len(parts) != arguments+1 {
		return InquiryAction{}, fmt.Errorf("%s requires %d IDs", result.Kind, arguments)
	}
	for index, value := range parts[1:] {
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return InquiryAction{}, fmt.Errorf("action ID %q must be a positive integer", value)
			}
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return InquiryAction{}, fmt.Errorf("action ID %q must be a positive integer", value)
		}
		if index == 0 {
			result.InquiryID = id
		} else {
			result.TargetID = id
		}
	}
	result.Body = strings.TrimSpace(strings.Join(sections["YOUR ANSWER"], "\n"))
	if result.Body == "" {
		return InquiryAction{}, fmt.Errorf("YOUR ANSWER requires nonempty text")
	}

	scores := map[string]float64{}
	for _, original := range sections["YOUR SCORE"] {
		line := workerControlText(original)
		if line == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		name, value = workerUnwrapControlText(name), workerUnwrapControlText(value)
		if !found || (name != "relevance" && name != "inquiry_value") {
			return InquiryAction{}, fmt.Errorf("YOUR SCORE requires relevance: and inquiry_value: lines; unexpected %q", line)
		}
		if _, exists := scores[name]; exists {
			return InquiryAction{}, fmt.Errorf("duplicate action score %q", name)
		}
		score, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return InquiryAction{}, fmt.Errorf("action score %s must be one finite number in [0,1]", name)
		}
		scores[name] = score
	}
	for _, name := range []string{"relevance", "inquiry_value"} {
		if _, exists := scores[name]; !exists {
			return InquiryAction{}, fmt.Errorf("missing action score %q", name)
		}
	}
	result.Relevance, result.InquiryValue = scores["relevance"], scores["inquiry_value"]
	return result, nil
}

// Read exactly two adjacent score slots backward from the reply tail. Cosmetic
// labels may precede a scalar, but prose is never skipped to find a number.
func workerPlainScoreTail(lines []string, actionLine int) (int, [2]string, error) {
	var values [2]string
	index, start := len(lines)-1, len(lines)
	for slot := 1; slot >= 0; slot-- {
		for index > actionLine && strings.TrimSpace(lines[index]) == "" {
			index--
		}
		if index <= actionLine {
			return 0, values, fmt.Errorf("plain action requires two final score values after its command")
		}
		value := workerControlText(lines[index])
		bare := false
		if label, number, found := strings.Cut(value, ":"); found {
			if strings.TrimSpace(label) == "" {
				return 0, values, fmt.Errorf("action line %d: score label must be nonempty when present", index+1)
			}
			value = number
		} else if fields := strings.Fields(value); len(fields) == 2 && workerScoreLabel(fields[0]) {
			value = fields[1]
		} else {
			_, err := strconv.ParseFloat(workerUnwrapControlText(value), 64)
			bare = err == nil
		}
		values[slot], start = value, index
		index--
		if bare {
			for index > actionLine && strings.TrimSpace(lines[index]) == "" {
				index--
			}
			if index > actionLine && workerScoreLabel(strings.TrimSuffix(workerControlText(lines[index]), ":")) {
				start = index
				index--
			}
		}
	}
	separator := start - 1
	for separator > actionLine && strings.TrimSpace(lines[separator]) == "" {
		separator--
	}
	if separator > actionLine && strings.TrimSpace(lines[separator]) == "---" {
		start = separator
	}
	return start, values, nil
}

func workerScoreLabel(text string) bool {
	text = workerUnwrapControlText(text)
	if text == "" {
		return false
	}
	if _, err := strconv.ParseFloat(text, 64); err == nil {
		return false
	}
	for index, char := range text {
		if !unicode.IsLetter(char) && char != '_' && !(index > 0 && (unicode.IsDigit(char) || char == '-')) {
			return false
		}
	}
	return true
}

// An explicit action marker selects the format. A first-line command may also
// identify a plain reply, with an optional cosmetic label. Legacy answer text
// cannot switch formats after its ACTION heading.
func workerPlainActionStart(lines []string, arities map[string]int) (int, string, bool) {
	first := true
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimSpace(line) == "# ACTION" {
			return 0, "", false
		}
		control := workerControlText(line)
		name, value, labeled := strings.Cut(control, ":")
		if labeled && strings.EqualFold(workerUnwrapControlText(name), "ACTION") {
			return index, value, true
		}
		if first {
			if labeled {
				control = workerControlText(value)
			}
			parts := strings.Fields(control)
			if len(parts) != 0 {
				if _, known := arities[workerUnwrapControlText(parts[0])]; known {
					return index, control, true
				}
			}
		}
		first = false
	}
	return 0, "", false
}

// Presentation is removed only from control text, never the answer or raw
// provider reply. The caller still checks exact vocabulary, arity and numbers.
func workerControlText(text string) string {
	text = strings.TrimSpace(text)
	rest := strings.TrimLeft(text, "#")
	level := len(text) - len(rest)
	if level >= 2 && level <= 6 && len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
		text = strings.TrimSpace(rest)
	}
	if len(text) > 1 && strings.ContainsRune("-*+", rune(text[0])) && (text[1] == ' ' || text[1] == '\t') {
		text = strings.TrimSpace(text[1:])
	}
	return workerUnwrapControlText(text)
}

func workerUnwrapControlText(text string) string {
	text = strings.TrimSpace(text)
	for {
		changed := false
		for _, wrapper := range []string{"**", "__", "`"} {
			if len(text) > 2*len(wrapper) && strings.HasPrefix(text, wrapper) && strings.HasSuffix(text, wrapper) &&
				strings.Index(text[len(wrapper):], wrapper) == len(text)-2*len(wrapper) {
				text = strings.TrimSpace(text[len(wrapper) : len(text)-len(wrapper)])
				changed = true
				break
			}
			// A common bold/code score label includes its colon: **relevance:** 0.8.
			if strings.HasPrefix(text, wrapper) {
				end := strings.Index(text[len(wrapper):], wrapper)
				if end >= 0 {
					label := text[len(wrapper) : len(wrapper)+end]
					if strings.HasSuffix(label, ":") {
						text = label + text[2*len(wrapper)+end:]
						changed = true
						break
					}
				}
			}
		}
		if !changed {
			return text
		}
	}
}
