package main

// QueenWorkerAction records the Queen's monitoring decision and her local
// self-assessment of that decision. These are not drone assessments of the
// decision or replacement grades for hypotheses under the accepted handoff.
// Scheduling one logical Queen and selecting unseen work belong to the runner.
type QueenWorkerAction struct {
	Kind         string  `json:"kind"`
	Body         string  `json:"body"`
	Relevance    float64 `json:"relevance"`
	InquiryValue float64 `json:"inquiry_value"`
}

// ParseQueenWorkerAction requires the same explicit answer and actual unit
// scores as drone actions, while allowing only monitoring decisions. Parsing
// a BEGIN_COLLAPSE decision establishes neither sufficient evidence nor success.
func ParseQueenWorkerAction(text string) (QueenWorkerAction, error) {
	action, err := parseScoredWorkerAction(text, map[string]int{
		"NOOP": 0, "GUIDE": 0, "BEGIN_COLLAPSE": 0,
	})
	if err != nil {
		return QueenWorkerAction{}, err
	}
	return QueenWorkerAction{
		Kind: action.Kind, Body: action.Body,
		Relevance: action.Relevance, InquiryValue: action.InquiryValue,
	}, nil
}
