package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ScoredText keeps a plain answer and its mandatory local actual scores.
// It carries neither a command nor a parent-hypothesis assessment.
type ScoredText struct {
	Body         string  `json:"body"`
	Relevance    float64 `json:"relevance"`
	InquiryValue float64 `json:"inquiry_value"`
}

func ParseScoredText(text string) (ScoredText, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start, values, err := workerPlainScoreTail(lines, -1)
	if err != nil {
		return ScoredText{}, fmt.Errorf("scored answer: %w", err)
	}
	result := ScoredText{Body: strings.TrimSpace(strings.Join(lines[:start], "\n"))}
	if result.Body == "" {
		return result, fmt.Errorf("scored answer requires nonempty text")
	}
	scores := []*float64{&result.Relevance, &result.InquiryValue}
	for i, value := range values {
		score, err := strconv.ParseFloat(strings.TrimSpace(workerUnwrapControlText(value)), 64)
		if err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return ScoredText{}, fmt.Errorf("local score %d must be one finite number in [0,1]", i+1)
		}
		*scores[i] = score
	}
	return result, nil
}
