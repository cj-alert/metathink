package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// These transports handle requests entirely in memory. The focused runner's
// network guard remains installed and no local or external server is contacted.
type v010ResearchTransport func(*http.Request) (*http.Response, error)

func (f v010ResearchTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type v010ResearchLog struct{ events []map[string]any }

func (l *v010ResearchLog) Log(scope, event string, fields map[string]any) {
	copy := map[string]any{"scope": scope, "event": event}
	for key, value := range fields {
		copy[key] = value
	}
	l.events = append(l.events, copy)
}

func v010ResearchClient(t *testing.T, log EventSink, transport v010ResearchTransport) *MetaClient {
	t.Helper()
	t.Setenv("METATHOUGHT_V010_RESEARCH_TEST_KEY", "v010-private-fixture-key")
	client, err := NewMetaClient(MetaConfig{BaseURL: "https://research-fixture.invalid/v1",
		APIKeyEnv: "METATHOUGHT_V010_RESEARCH_TEST_KEY", ReasoningEffort: "low",
		ModelReasoningEffort: map[string]string{"requested-model": "high"}}, log)
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP.Transport = transport
	return client
}

func v010ResearchReply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestV010ResearchTransportRequestAndOriginalEvidence(t *testing.T) {
	search := `{"id":"search-1", "type":"web_search_call","status":"completed","results":[{"type":"text_result","url":"https://source.invalid/paper","title":"Original α","snippet":"Exact result","future_field":{"keep":true}}]}`
	citation := `{"type":"url_citation", "url":"https://source.invalid/paper","title":"Original α","start_index":0,"end_index":6,"future_field":7}`
	body := `{"model":"actual-provider-model","status":"completed","output":[{"type":"reasoning","summary":[]},{"type":"message","role":"assistant","phase":"commentary","status":"completed","content":[{"type":"output_text","text":"I will search first."}]},` + search + `,{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Answer α\r\n ","annotations":[` + citation + `]}]}],"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":8,"cache_write_tokens":3},"output_tokens":9,"output_tokens_details":{"reasoning_tokens":4},"total_tokens":29}}`
	log := &v010ResearchLog{}
	calls := 0
	var sent map[string]any
	client := v010ResearchClient(t, log, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer v010-private-fixture-key" {
			t.Fatal("research route or authentication changed")
		}
		if err := json.NewDecoder(request.Body).Decode(&sent); err != nil {
			t.Fatal(err)
		}
		return v010ResearchReply(200, body), nil
	})
	cache, safety := "research-scope", "fixture-user"
	got, err := client.Complete(context.Background(), TextRequest{Model: "requested-model", Scope: "drone", WebSearch: true,
		PromptCacheKey: &cache, SafetyIdentifier: &safety,
		Messages: []Message{{"role": "developer", "content": "Find sources."}, {"role": "user", "content": "Exact question α\r\n "}}})
	if err != nil || calls != 1 || got.Text != "Answer α\r\n " || got.Model != "actual-provider-model" || got.Refusal != "" {
		t.Fatalf("research answer: %#v, calls=%d, err=%v", got, calls, err)
	}
	if sent["model"] != "requested-model" || sent["prompt_cache_key"] != cache || sent["safety_identifier"] != safety {
		t.Fatal("request identity/options lost")
	}
	if !reflect.DeepEqual(sent["tools"], []any{map[string]any{"type": "web_search"}}) ||
		!reflect.DeepEqual(sent["include"], []any{"web_search_call.results"}) ||
		!reflect.DeepEqual(sent["reasoning"], map[string]any{"effort": "high"}) {
		t.Fatalf("incorrect Responses controls: %#v", sent)
	}
	wantInput := []any{
		map[string]any{"role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "Find sources."}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Exact question α\r\n "}}},
	}
	if !reflect.DeepEqual(sent["input"], wantInput) {
		t.Fatalf("input text changed: %#v", sent["input"])
	}
	for _, key := range []string{"messages", "reasoning_effort", "max_tokens", "max_completion_tokens", "max_output_tokens", "response_format"} {
		if _, exists := sent[key]; exists {
			t.Fatalf("unexpected Responses field %s", key)
		}
	}
	if string(got.Raw) != body || len(got.SearchCalls) != 1 || string(got.SearchCalls[0]) != search || len(got.Citations) != 1 || string(got.Citations[0]) != citation {
		t.Fatal("original provider evidence was altered or omitted")
	}
	if got.Usage.InputTokens == nil || *got.Usage.InputTokens != 20 || *got.Usage.OutputTokens != 9 || *got.Usage.TotalTokens != 29 ||
		*got.Usage.InputTokensDetails.CachedTokens != 8 || *got.Usage.InputTokensDetails.CacheWriteTokens != 3 || *got.Usage.OutputTokensDetails.ReasoningTokens != 4 {
		t.Fatalf("Responses usage was lost: %#v", got.Usage)
	}
	logged, _ := json.Marshal(log.events)
	if strings.Contains(string(logged), "v010-private-fixture-key") || len(log.events) != 3 ||
		log.events[0]["event"] != "llm.start" || log.events[1]["event"] != "llm.response" || log.events[2]["event"] != "llm.finish" ||
		log.events[2]["scope"] != "drone" || log.events[2]["search_call_count"] != 1 {
		t.Fatal("research diagnostic attribution or credential redaction failed")
	}
}

func TestV010ResearchTransportChatCompatibility(t *testing.T) {
	var sent map[string]any
	client := v010ResearchClient(t, nil, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatal("ordinary call moved endpoints")
		}
		if err := json.NewDecoder(request.Body).Decode(&sent); err != nil {
			t.Fatal(err)
		}
		return v010ResearchReply(200, `{"model":"chat-model","choices":[{"finish_reason":"stop","message":{"content":"ordinary answer"}}]}`), nil
	})
	got, err := client.Complete(context.Background(), TextRequest{Model: "requested-model", ReasoningEffort: "medium", Messages: []Message{{"role": "user", "content": "ordinary question"}}})
	if err != nil || got.Text != "ordinary answer" || got.Usage.TotalTokens != nil || len(got.SearchCalls) != 0 || len(got.Citations) != 0 || sent["reasoning_effort"] != "medium" {
		t.Fatalf("ordinary call changed: %#v, %v", got, err)
	}
	if _, ok := sent["messages"]; !ok {
		t.Fatal("ordinary messages missing")
	}
	for _, key := range []string{"input", "tools", "include", "reasoning", "max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if _, exists := sent[key]; exists {
			t.Fatalf("ordinary call acquired field %s", key)
		}
	}
}

func TestV010ResearchTransportOutcomesPreserveEvidence(t *testing.T) {
	search := `{"type":"web_search_call","id":"search-failure","status":"failed","results":[]}`
	for _, tc := range []struct {
		name, body, kind, refusal, text string
		status, searches                int
	}{
		{"declined-search", `{"model":"meta","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"No search performed."}]}]}`, "", "", "No search performed.", 200, 0},
		{"refusal", `{"model":"meta","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"Cannot research this."}]}]}`, "", "Cannot research this.", "", 200, 0},
		{"search-failed", `{"model":"meta","status":"completed","output":[` + search + `,{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Search failed."}]}]}`, "", "", "Search failed.", 200, 1},
		{"incomplete", `{"model":"meta","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[` + search + `],"usage":{"input_tokens":5,"output_tokens":2}}`, "incomplete_output", "", "", 200, 1},
		{"failed-response", `{"model":"meta","status":"failed","error":{"code":"search_unavailable","message":"Search service failed."},"output":[` + search + `]}`, "api_rejection", "", "", 200, 1},
		{"commentary-only", `{"model":"meta","status":"completed","output":[{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Still looking."}]}]}`, "format_error", "", "", 200, 0},
		{"http-rejection", `{"error":{"code":"invalid_key","message":"Key v010-private-fixture-key rejected."}}`, "api_rejection", "", "", 401, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			log := &v010ResearchLog{}
			client := v010ResearchClient(t, log, func(*http.Request) (*http.Response, error) {
				calls++
				return v010ResearchReply(tc.status, tc.body), nil
			})
			got, err := client.Complete(context.Background(), TextRequest{Model: "requested-model", WebSearch: true, Messages: []Message{{"role": "user", "content": "research"}}})
			if calls != 1 || got.Text != tc.text || got.Refusal != tc.refusal || len(got.SearchCalls) != tc.searches || string(got.Raw) != strings.ReplaceAll(tc.body, "v010-private-fixture-key", "[REDACTED]") {
				t.Fatalf("outcome/evidence mismatch: %#v, calls=%d, err=%v", got, calls, err)
			}
			var provider *ProviderError
			if tc.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &provider) || provider.Kind != tc.kind {
				t.Fatalf("expected %s, got %v", tc.kind, err)
			}
			if tc.name == "failed-response" && (provider.ProviderCode != "search_unavailable" || provider.Message != "Search service failed.") {
				t.Fatal("provider's response error was altered")
			}
			if tc.name == "incomplete" {
				if got.Usage.InputTokens == nil || *got.Usage.InputTokens != 5 || *got.Usage.OutputTokens != 2 || got.Usage.TotalTokens != nil {
					t.Fatal("incomplete call usage lost or missing usage invented")
				}
			}
			logged, _ := json.Marshal(log.events)
			if strings.Contains(string(logged), "v010-private-fixture-key") || (err != nil && strings.Contains(err.Error(), "v010-private-fixture-key")) {
				t.Fatal("credential leaked on error path")
			}
		})
	}
}

func TestV010ResearchTransportRejectsUnsupportedInputAndHonorsCancellation(t *testing.T) {
	calls := 0
	client := v010ResearchClient(t, nil, func(request *http.Request) (*http.Response, error) {
		calls++
		return nil, request.Context().Err()
	})
	for _, message := range []Message{
		{"role": "tool", "content": "tool result"},
		{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": "fixture"}}},
		{"role": "user", "content": "text", "name": "extra semantics"},
	} {
		_, err := client.Complete(context.Background(), TextRequest{Model: "meta", WebSearch: true, Messages: []Message{message}})
		var provider *ProviderError
		if !errors.As(err, &provider) || provider.Kind != "invalid_request" || calls != 0 {
			t.Fatalf("unsupported message was sent/dropped: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Complete(ctx, TextRequest{Model: "meta", WebSearch: true, Messages: []Message{{"role": "user", "content": "question"}}})
	var provider *ProviderError
	if !errors.As(err, &provider) || provider.Kind != "transport_error" || !strings.Contains(provider.Message, context.Canceled.Error()) || calls != 1 {
		t.Fatalf("caller cancellation changed: calls=%d, err=%v", calls, err)
	}
}
