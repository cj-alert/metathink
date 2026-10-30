package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func v15PlainReply(command, body, scores string) string {
	return "ACTION: " + command + "\n" + body + "\n" + scores
}

func TestV015PlainFooterRemovesOnlySeparatorAdjacentToScores(t *testing.T) {
	body := "Original finding.\n---\nBasis: the earlier horizontal rule belongs to the answer."
	for _, tail := range []string{"0.2\n0.8", "relevance 0.2\ninquiry_value 0.8", "relevancy\n0.2\ninquiryValue\n0.8"} {
		got, err := ParseInquiryAction(v15PlainReply("PASS", body+"\n---", tail))
		if err != nil || got.Body != body || got.Relevance != 0.2 || got.InquiryValue != 0.8 {
			t.Fatalf("footer changed original body or positional scores: %+v error=%v", got, err)
		}
		queen, err := ParseQueenWorkerAction(v15PlainReply("GUIDE", body+"\n---", tail))
		if err != nil || queen.Body != body || queen.Relevance != 0.2 || queen.InquiryValue != 0.8 {
			t.Fatalf("Queen footer changed original body or scores: %+v error=%v", queen, err)
		}
		without, err := ParseInquiryAction(v15PlainReply("PASS", body, tail))
		if err != nil || !reflect.DeepEqual(without, got) {
			t.Fatalf("optional footer changed action meaning: with=%+v without=%+v error=%v", got, without, err)
		}
	}
}

func TestV015PlainActionPreservesExplanationBeforePositionalEndpointScores(t *testing.T) {
	reply := "Basis: before the command.\naction: CONNECT 2 3\nOriginal link, with α and uncertainty.\nrelevance: 0.25\nBasis: prior score-like text is evidence, not the tail.\nrelevancy: 0\n\ninquiryValue: 1\n"
	wantBody := "Basis: before the command.\nOriginal link, with α and uncertainty.\nrelevance: 0.25\nBasis: prior score-like text is evidence, not the tail."
	got, err := ParseInquiryAction(reply)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "CONNECT" || got.InquiryID != 2 || got.TargetID != 3 || got.Body != wantBody || got.Relevance != 0 || got.InquiryValue != 1 {
		t.Fatalf("plain action lost original explanation or explicit endpoint scores: %+v", got)
	}
	queen, err := ParseQueenWorkerAction(strings.Replace(reply, "action: CONNECT 2 3", "aCtIoN: GUIDE", 1))
	if err != nil || queen.Kind != "GUIDE" || queen.Body != wantBody || queen.Relevance != 0 || queen.InquiryValue != 1 {
		t.Fatalf("Queen does not use the same plain envelope: %+v error=%v", queen, err)
	}
}

func TestV015PlainScoreLabelsDoNotOverrideOrderAndSimpleCommandsNeedNoHeading(t *testing.T) {
	body := "Basis: there are 12 observations and 0.95 is a cited estimate."
	for name, scores := range map[string]string{
		"bare": "0.2\n0.8",
		"misspelled": "relevancy: 0.2\ninquiryValue: 0.8",
		"arbitrary": "First estimate: 0.2\nSecond estimate: 0.8",
		"swapped_words": "inquiry_value: 0.2\nrelevance: 0.8",
		"repeated_label": "score: 0.2\nscore: 0.8",
	} {
		t.Run(name, func(t *testing.T) {
			for _, command := range []string{"ACTION: PASS", "action: PASS", "PASS", "Decision: PASS"} {
				got, err := ParseInquiryAction(command + "\n" + body + "\n" + scores)
				if err != nil || got.Kind != "PASS" || got.Body != body || got.Relevance != 0.2 || got.InquiryValue != 0.8 {
					t.Fatalf("labels or body numbers overrode positional scores: command=%q action=%+v error=%v", command, got, err)
				}
			}
			queen, err := ParseQueenWorkerAction("GUIDE\n" + body + "\n" + scores)
			if err != nil || queen.Body != body || queen.Relevance != 0.2 || queen.InquiryValue != 0.8 {
				t.Fatalf("Queen positional scores differ: %+v error=%v", queen, err)
			}
		})
	}
}

func TestV015PlainAdjacentLabelsAcceptObservedCosmeticVariants(t *testing.T) {
	body := "Original reasoning cites 0.44 as evidence.\nBasis: this sentence must remain intact."
	cases := []struct {
		name, tail string
		relevance, value float64
	}{
		{"live_separate_labels", "relevance\n0.85\ninquiry_value\n0.65", 0.85, 0.65},
		{"live_space_labels", "relevance 0.92\ninquiry_value 0.82", 0.92, 0.82},
		{"misspelled_separate_labels", "relevancy\n0.2\ninquiryValue\n0.8", 0.2, 0.8},
		{"misspelled_space_labels", "relevancy 0.2\ninquiryValue 0.8", 0.2, 0.8},
		{"mixed_colon_separate", "first_score: 0.2\nsecond-score\n0.8", 0.2, 0.8},
		{"separate_colon_and_space", "relevancy:\n0.2\ninquiryValue 0.8", 0.2, 0.8},
		{"endpoint_whitespace", "relevance\n\n0\ninquiry_value\n\n1\n", 0, 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseInquiryAction(v15PlainReply("PASS", body, test.tail))
			if err != nil || got.Body != body || got.Relevance != test.relevance || got.InquiryValue != test.value {
				t.Fatalf("cosmetic tail changed the body or actual scores: %+v error=%v", got, err)
			}
			queen, err := ParseQueenWorkerAction(v15PlainReply("GUIDE", body, test.tail))
			if err != nil || queen.Body != body || queen.Relevance != test.relevance || queen.InquiryValue != test.value {
				t.Fatalf("Queen rejected cosmetic tail or changed its meaning: %+v error=%v", queen, err)
			}
		})
	}
}

func TestV015PlainAdjacentLabelsDoNotFindMissingScoresInsideProse(t *testing.T) {
	body := "A cited prior value was 0.44.\nrelevance: 0.25\nBasis: keep this explanation and prior numeric evidence."
	cases := map[string]string{
		"missing_first_value": "relevance\ninquiry_value\n0.8",
		"missing_last_value": "relevance\n0.2\ninquiry_value",
		"no_scan_over_prose": "relevance\n0.2\nBasis: explanation interrupts the score tail.\ninquiry_value\n0.8",
		"numeric_prose_not_label": "The evidence suggests 0.2\ninquiry_value 0.8",
		"trailing_prose": "relevance\n0.2\ninquiry_value\n0.8\nBasis: not an actual score field.",
		"expression_not_scalar": "relevance 0.2+0.3\ninquiry_value 0.8",
		"attached_reason": "relevance 0.2 because relevant\ninquiry_value 0.8",
	}
	for _, value := range []string{"NaN", "Inf", "-Inf", "-0.01", "1.01"} {
		cases["separate_invalid_"+value] = "relevancy\n" + value + "\ninquiryValue\n0.8"
		cases["space_invalid_"+value] = "relevancy 0.2\ninquiryValue " + value
	}
	for name, tail := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseInquiryAction(v15PlainReply("PASS", body, tail)); err == nil {
				t.Fatalf("invalid tail borrowed a number from prose or accepted a bad value: %+v", got)
			}
			if got, err := ParseQueenWorkerAction(v15PlainReply("GUIDE", body, tail)); err == nil {
				t.Fatalf("Queen accepted invalid tail: %+v", got)
			}
		})
	}
}

func TestV015PlainActionRejectsMissingAmbiguousAndNonNumericFields(t *testing.T) {
	valid := v15PlainReply("PASS", "An original explanation.", "relevance: 0.5\ninquiry_value: 0.75")
	cases := map[string]string{
		"missing_action":         strings.Replace(valid, "ACTION: PASS\n", "", 1),
		"empty_action":           strings.Replace(valid, "ACTION: PASS", "ACTION:", 1),
		"duplicate_action":       "ACTION: PASS\n" + valid,
		"duplicate_empty_action": "ACTION:\n" + valid,
		"missing_relevance":      strings.Replace(valid, "relevance: 0.5\n", "", 1),
		"missing_value":          strings.Replace(valid, "inquiry_value: 0.75", "", 1),
		"explanation_after_pair": valid + "\nBasis: prose is not a numeric tail slot.",
		"explanation_between_pair": strings.Replace(valid, "inquiry_value:", "Basis: keep explanation before both scores.\ninquiry_value:", 1),
		"missing_label_before_colon": strings.Replace(valid, "relevance:", ":", 1),
		"empty_body":             "ACTION: PASS\nrelevance: 0\ninquiry_value: 1",
		"whitespace_body":        "ACTION: PASS\n \t \nrelevance: 0\ninquiry_value: 1",
	}
	for _, value := range []string{"", "NaN", "Inf", "+Inf", "-Inf", "-0.01", "1.01", "0.7 because useful", "0.7/1", "0.7 0.8"} {
		cases["bad_relevance_"+value] = strings.Replace(valid, "relevance: 0.5", "relevance: "+value, 1)
		cases["bad_value_"+value] = strings.Replace(valid, "inquiry_value: 0.75", "inquiry_value: "+value, 1)
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseInquiryAction(reply); err == nil {
				t.Fatalf("invalid plain reply was accepted: %+v", got)
			}
			queenReply := strings.ReplaceAll(reply, "ACTION: PASS", "ACTION: GUIDE")
			if got, err := ParseQueenWorkerAction(queenReply); err == nil {
				t.Fatalf("Queen accepted the same invalid envelope: %+v", got)
			}
		})
	}
}

func TestV015PlainActionsRetainVocabularyArityIDsAndLegacyCompatibility(t *testing.T) {
	body := "Original paragraph.\n\n## Basis\nAn exact explanation, not a replacement score."
	scores := "relevance: 0\ninquiry_value: 1"
	for _, command := range []string{"PROPOSE", "JOIN 2", "CONTRIBUTE 2", "CONNECT 2 3", "PASS", "SPAWN 2", "DISCOVER", "READ 4", "RESEARCH 2"} {
		t.Run("drone_"+command, func(t *testing.T) {
			plain, err := ParseInquiryAction(v15PlainReply(command, body, scores))
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := ParseInquiryAction(v9Reply(command, body, 0, 1))
			if err != nil || !reflect.DeepEqual(plain, legacy) || plain.Body != body {
				t.Fatalf("plain and saved legacy meanings differ: plain=%+v legacy=%+v error=%v", plain, legacy, err)
			}
		})
	}
	for _, command := range []string{"NOOP", "GUIDE", "BEGIN_COLLAPSE"} {
		plain, err := ParseQueenWorkerAction(v15PlainReply(command, body, scores))
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := ParseQueenWorkerAction(v9Reply(command, body, 0, 1))
		if err != nil || !reflect.DeepEqual(plain, legacy) || plain.Body != body {
			t.Fatalf("Queen legacy meaning changed for %s: plain=%+v legacy=%+v error=%v", command, plain, legacy, err)
		}
		if _, err := ParseInquiryAction(v15PlainReply(command, body, scores)); err == nil {
			t.Fatalf("drone accepted Queen action %s", command)
		}
	}
	for _, command := range []string{"JOIN", "JOIN 2 3", "JOIN 0", "JOIN -1", "JOIN +1", "JOIN 1.5", "JOIN 9223372036854775808", "CONNECT 2", "PASS 1", "SPAWN", "READ 0", "RESEARCH 2 3", "UNKNOWN"} {
		if got, err := ParseInquiryAction(v15PlainReply(command, body, scores)); err == nil {
			t.Fatalf("invalid drone command %q accepted: %+v", command, got)
		}
	}
	for _, command := range []string{"JOIN 2", "PASS", "RETURN", "GUIDE 1", "NOOP 1", "BEGIN_COLLAPSE 1", "UNKNOWN"} {
		if got, err := ParseQueenWorkerAction(v15PlainReply(command, body, scores)); err == nil {
			t.Fatalf("invalid Queen command %q accepted: %+v", command, got)
		}
	}
}

func TestV015PlainMalformedDroneReplyDoesNotAcceptWorkOrMembership(t *testing.T) {
	c, dir := v10Config(t, 2, 1), t.TempDir()
	calls := 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls++
		if request.Scope != "drone" {
			return TextResponse{}, errors.New("no Queen or extra model call is expected before first accepted work")
		}
		return TextResponse{Text: "ACTION: JOIN 1\nA possible contribution.\nrelevance: 0.8"}, nil
	})
	result, err := RunWorkerExperiment(v10Context(t), c, dir, v9Handoff, client)
	if err == nil || result.Status != "failed" || calls != 1 {
		t.Fatalf("malformed first action did not stop as an unaccepted reply: result=%+v calls=%d error=%v", result, calls, err)
	}
	db := v9ReadDB(t, filepath.Join(dir, c.Files.State))
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work") != 0 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_members") != 0 {
		t.Fatal("malformed plain action persisted accepted work or a commitment")
	}
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_worker_queue WHERE phase<>'contribute'") != 0 {
		t.Fatal("malformed action advanced a drone's thinking phase")
	}
}

func TestV015PlainQueenGuidancePersistsOriginalBodyAndRejectsIncompleteScores(t *testing.T) {
	for _, valid := range []bool{true, false} {
		name := "complete"
		if !valid {
			name = "missing_score"
		}
		t.Run(name, func(t *testing.T) {
			body := "Basis: retain both alternatives.\nAsk for evidence distinguishing them.\nBasis: uncertainty remains."
			reply := "Basis: retain both alternatives.\nACTION: GUIDE\nAsk for evidence distinguishing them.\nBasis: uncertainty remains.\nrelevancy\n0\ninquiryValue 1"
			if !valid {
				reply = strings.Replace(reply, "\ninquiryValue 1", "", 1)
			}
			calls := 0
			client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				calls++
				if request.Scope != "queen" {
					return TextResponse{}, errors.New("only the Queen monitor is under test")
				}
				return TextResponse{Text: reply}, nil
			})
			h, _, _ := v10Fixture(t, 2, client)
			workID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", 1, 1, "contribute", "CONTRIBUTE", "Original inspected evidence.", 0.8, 0.7, now())
			result, err := h.MonitorWorkerQueen(v10Context(t), "plain-queen")
			if calls != 1 || !result.Called || result.BeginCollapse {
				t.Fatalf("unexpected monitor dispatch: result=%+v calls=%d error=%v", result, calls, err)
			}
			if !valid {
				if err == nil || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_guidance") != 0 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queen_seen") != 0 {
					t.Fatalf("incomplete scores were accepted as guidance or consumed inspected work: error=%v", err)
				}
				if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_requests WHERE status='working'") != 1 {
					t.Fatal("invalid Queen reply changed the request's decision state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var savedBody, note, owner string
			var relevance, value float64
			if err = h.Store.db.QueryRow("SELECT body,relevance,inquiry_value FROM hive_worker_guidance WHERE request_id=? AND inquiry_id=1", h.requestID).Scan(&savedBody, &relevance, &value); err != nil || savedBody != body || relevance != 0 || value != 1 {
				t.Fatalf("guidance lost original body or zero score: body=%q scores=%v/%v error=%v", savedBody, relevance, value, err)
			}
			if err = h.Store.db.QueryRow("SELECT note,owner FROM hive_worker_queen WHERE request_id=?", h.requestID).Scan(&note, &owner); err != nil || note != body || owner != "" {
				t.Fatalf("accepted guidance did not finish its Queen claim: note=%q owner=%q error=%v", note, owner, err)
			}
			var through int64
			if err = h.Store.db.QueryRow("SELECT last_work_id FROM hive_worker_queen_seen WHERE request_id=? AND inquiry_id=1", h.requestID).Scan(&through); err != nil || through != workID {
				t.Fatalf("accepted guidance did not consume exactly its inspected evidence: through=%d error=%v", through, err)
			}
		})
	}
}
