package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const v010RetryKey = "v010-retry-private-key"

func v010RetryClient(t *testing.T, retry *HTTP500RetryConfig, log EventSink, transport v010ResearchTransport) *MetaClient {
	t.Helper()
	t.Setenv("METATHOUGHT_V010_RETRY_TEST_KEY", v010RetryKey)
	client, err := NewMetaClient(MetaConfig{BaseURL: "https://retry-fixture.invalid/v1", APIKeyEnv: "METATHOUGHT_V010_RETRY_TEST_KEY", ReasoningEffort: "high", HTTP500Retry: retry}, log)
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP.Transport = transport
	return client
}

func v010RetryRequest(research bool) TextRequest {
	return TextRequest{Model: "requested-model", Scope: "drone", WebSearch: research, Messages: []Message{{"role": "developer", "content": "Keep this exact instruction."}, {"role": "user", "content": "Exact question α\r\n "}}}
}

func v010RetrySuccess(research bool) string {
	if research {
		return `{"model":"actual-model","status":"completed","output":[{"type":"web_search_call","id":"original-search","status":"completed","results":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Answer α"}]}],"usage":{"input_tokens":12,"output_tokens":8,"total_tokens":20}}`
	}
	return `{"model":"actual-model","choices":[{"finish_reason":"stop","message":{"content":"Answer α"}}],"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`
}

func v010RetryEventCount(log *v010ResearchLog, event string) int {
	n := 0
	for _, saved := range log.events {
		if saved["event"] == event {
			n++
		}
	}
	return n
}

func TestV010HTTP500RetryUsesIdenticalRequestsOnBothEndpoints(t *testing.T) {
	for _, research := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "responses"}[research], func(t *testing.T) {
			log := &v010ResearchLog{}
			var original []byte
			calls := 0
			client := v010RetryClient(t, &HTTP500RetryConfig{MaxAttempts: 3, DelayMS: 0}, log, func(request *http.Request) (*http.Response, error) {
				calls++
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if calls == 1 {
					original = body
				} else if !bytes.Equal(body, original) {
					t.Fatal("retry changed the serialized request")
				}
				wantPath := "/v1/chat/completions"
				if research {
					wantPath = "/v1/responses"
				}
				if request.URL.Path != wantPath || request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer "+v010RetryKey {
					t.Fatal("retry changed endpoint, method or authentication")
				}
				if calls == 1 {
					return v010ResearchReply(500, `{"error":{"code":"transient","message":"internal server error `+v010RetryKey+`"}}`), nil
				}
				return v010ResearchReply(200, v010RetrySuccess(research)), nil
			})
			got, err := client.Complete(context.Background(), v010RetryRequest(research))
			if err != nil || calls != 2 || got.Text != "Answer α" || got.Model != "actual-model" || got.Usage.TotalTokens == nil || *got.Usage.TotalTokens != 20 || string(got.Raw) != v010RetrySuccess(research) {
				t.Fatalf("recovered response lost data: %+v calls=%d error=%v", got, calls, err)
			}
			if research && (len(got.SearchCalls) != 1 || !strings.Contains(string(got.SearchCalls[0]), "original-search")) {
				t.Fatal("recovered research lost original search evidence")
			}
			for event, want := range map[string]int{"llm.start": 1, "llm.finish": 1, "llm.response": 2, "llm.retry.scheduled": 1, "llm.retry.recovered": 1, "llm.retry.exhausted": 0} {
				if got := v010RetryEventCount(log, event); got != want {
					t.Fatalf("%s count=%d want=%d", event, got, want)
				}
			}
			attempt := 0
			for _, event := range log.events {
				if event["event"] == "llm.response" {
					attempt++
					if event["attempt"] != attempt || event["body_complete"] != true || event["duration_ms"] == nil || event["body"] == "" || event["status_code"] != map[int]int{1: 500, 2: 200}[attempt] {
						t.Fatalf("attempt evidence missing: %+v", event)
					}
				}
			}
			logged, _ := json.Marshal(log.events)
			if strings.Contains(string(logged), v010RetryKey) {
				t.Fatal("retry logs leaked the credential")
			}
		})
	}
}

func TestV010HTTP500RetryExhaustionPreservesProviderFailure(t *testing.T) {
	for _, research := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "responses"}[research], func(t *testing.T) {
			calls := 0
			log := &v010ResearchLog{}
			var original []byte
			client := v010RetryClient(t, &HTTP500RetryConfig{MaxAttempts: 3}, log, func(request *http.Request) (*http.Response, error) {
				calls++
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if calls == 1 {
					original = body
				} else if !bytes.Equal(body, original) {
					t.Fatal("failed attempt changed the request")
				}
				return v010ResearchReply(500, `{"error":{"code":"upstream_500","message":"internal server error"}}`), nil
			})
			out, err := client.Complete(context.Background(), v010RetryRequest(research))
			var provider *ProviderError
			if !errors.As(err, &provider) || provider.Kind != "retry_exhausted" || provider.StatusCode != 500 || provider.Attempts != 3 || provider.ProviderCode != "upstream_500" || provider.Message != "internal server error" || calls != 3 {
				t.Fatalf("exhaustion lost exact provider failure: %+v calls=%d error=%v", provider, calls, err)
			}
			if !strings.Contains(string(out.Raw), "internal server error") || v010RetryEventCount(log, "llm.start") != 1 || v010RetryEventCount(log, "llm.finish") != 1 || v010RetryEventCount(log, "llm.retry.scheduled") != 2 || v010RetryEventCount(log, "llm.retry.exhausted") != 1 || v010RetryEventCount(log, "llm.retry.recovered") != 0 {
				t.Fatal("exhaustion evidence was missing or logical calls were duplicated")
			}
		})
	}
}

type v010RetryBrokenBody struct{}

func (v010RetryBrokenBody) Read(p []byte) (int, error) {
	return copy(p, "partial "+v010RetryKey), io.ErrUnexpectedEOF
}
func (v010RetryBrokenBody) Close() error { return nil }

func TestV010HTTP500RetryDoesNotReplayOtherOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, kind                string
		status                    int
		body                      string
		transportError, readError bool
	}{
		{name: "401", kind: "api_rejection", status: 401, body: `{"error":"unauthorized"}`},
		{name: "429", kind: "api_rejection", status: 429, body: `{"error":"rate limit"}`},
		{name: "502", kind: "api_rejection", status: 502, body: `{"error":"bad gateway"}`},
		{name: "transport uncertainty", kind: "transport_error", transportError: true},
		{name: "incomplete 500 body", kind: "transport_error", status: 500, readError: true},
		{name: "200 refusal", status: 200, body: `{"model":"meta","choices":[{"message":{"refusal":"Cannot answer."}}]}`},
		{name: "200 invalid format", kind: "format_error", status: 200, body: `{"unexpected":"shape"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := v010RetryClient(t, &HTTP500RetryConfig{MaxAttempts: 3}, nil, func(*http.Request) (*http.Response, error) {
				calls++
				if tc.transportError {
					return nil, errors.New("uncertain transport " + v010RetryKey)
				}
				if tc.readError {
					return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: v010RetryBrokenBody{}}, nil
				}
				return v010ResearchReply(tc.status, tc.body), nil
			})
			out, err := client.Complete(context.Background(), v010RetryRequest(false))
			if calls != 1 {
				t.Fatalf("non-retryable outcome replayed %d times", calls)
			}
			var provider *ProviderError
			if tc.kind == "" {
				if err != nil || out.Refusal != "Cannot answer." {
					t.Fatalf("refusal changed: %+v error=%v", out, err)
				}
			} else if !errors.As(err, &provider) || provider.Kind != tc.kind || provider.Attempts != 1 || strings.Contains(err.Error(), v010RetryKey) {
				t.Fatalf("wrong non-retryable failure: %+v error=%v", provider, err)
			}
		})
	}
}

type v010RetrySignalLog struct{ scheduled chan struct{} }

func (l *v010RetrySignalLog) Log(_, event string, _ map[string]any) {
	if event == "llm.retry.scheduled" {
		select {
		case l.scheduled <- struct{}{}:
		default:
		}
	}
}

func TestV010HTTP500RetryWaitHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &v010RetrySignalLog{scheduled: make(chan struct{}, 1)}
	calls := 0
	client := v010RetryClient(t, &HTTP500RetryConfig{MaxAttempts: 3, DelayMS: 30000}, log, func(*http.Request) (*http.Response, error) {
		calls++
		return v010ResearchReply(500, `{"error":"internal server error"}`), nil
	})
	done := make(chan error, 1)
	go func() { _, err := client.Complete(ctx, v010RetryRequest(false)); done <- err }()
	select {
	case <-log.scheduled:
	case <-time.After(2 * time.Second):
		t.Fatal("retry was not scheduled")
	}
	// Let the fixed 30-second provider-failure wait begin, then cancel it.
	time.AfterFunc(20*time.Millisecond, cancel)
	select {
	case err := <-done:
		var provider *ProviderError
		if !errors.As(err, &provider) || provider.Kind != "transport_error" || provider.Message != context.Canceled.Error() || provider.Attempts != 1 || calls != 1 {
			t.Fatalf("cancelled wait replayed or obscured cancellation: %+v calls=%d error=%v", provider, calls, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller cancellation did not interrupt the retry wait")
	}
}

func TestV010HTTP500RetryConfigurationAndLegacySingleAttempt(t *testing.T) {
	for _, invalid := range []*HTTP500RetryConfig{{MaxAttempts: 0}, {MaxAttempts: -1}, {MaxAttempts: 1, DelayMS: -1}} {
		if _, err := NewMetaClient(MetaConfig{BaseURL: "https://fixture.invalid", HTTP500Retry: invalid}, nil); err == nil {
			t.Fatalf("invalid retry configuration accepted: %+v", invalid)
		}
	}
	overflow := int64((1<<63-1)/time.Millisecond) + 1
	if int64(int(overflow)) == overflow {
		if err := (&HTTP500RetryConfig{MaxAttempts: 1, DelayMS: int(overflow)}).validate(); err == nil {
			t.Fatal("retry delay duration overflow accepted")
		}
	}
	for _, tc := range []struct {
		name   string
		config *HTTP500RetryConfig
		kind   string
	}{{"omitted", nil, "api_rejection"}, {"explicit one attempt", &HTTP500RetryConfig{MaxAttempts: 1}, "retry_exhausted"}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := v010RetryClient(t, tc.config, nil, func(*http.Request) (*http.Response, error) {
				calls++
				return v010ResearchReply(500, `{"error":"internal server error"}`), nil
			})
			_, err := client.Complete(context.Background(), v010RetryRequest(false))
			var provider *ProviderError
			if !errors.As(err, &provider) || provider.Kind != tc.kind || provider.Attempts != 1 || calls != 1 {
				t.Fatalf("single-attempt contract changed: %+v calls=%d error=%v", provider, calls, err)
			}
		})
	}
}
