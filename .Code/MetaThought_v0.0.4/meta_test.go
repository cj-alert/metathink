package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type metaTestLog struct{ events []map[string]any }

func (l *metaTestLog) Log(scope, event string, fields map[string]any) {
	copy := map[string]any{"scope": scope, "event": event}
	for k, v := range fields {
		copy[k] = v
	}
	l.events = append(l.events, copy)
}

func metaTestConfig(base string) MetaConfig {
	return MetaConfig{BaseURL: base, APIKeyEnv: "METATHOUGHT_TEST_KEY"}
}

func TestMetaHTTPAndTelemetry(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "private-test-key")
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Error("wrong route or auth")
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"actual-meta","choices":[{"finish_reason":"stop","message":{"content":"Answer","refusal":null}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":3}}}`))
	}))
	defer server.Close()
	log := &metaTestLog{}
	client, err := NewMetaClient(metaTestConfig(server.URL), log)
	if err != nil {
		t.Fatal(err)
	}
	if client.HTTP.Timeout != 0 {
		t.Fatal("client must not impose an elapsed-time ceiling")
	}
	got, err := client.Complete(context.Background(), TextRequest{Model: "requested-meta", Scope: "mq", PromptCacheKey: decisionTestString("group-a"), SafetyIdentifier: decisionTestString("participant-1"), Messages: []Message{{"role": "user", "content": "hello"}}})
	if err != nil || got.Text != "Answer" || got.Model != "actual-meta" || *got.Usage.InputTokens != 11 || *got.Usage.InputTokensDetails.CachedTokens != 2 || got.Usage.InputTokensDetails.CacheWriteTokens != nil {
		t.Fatalf("response %#v, error %v", got, err)
	}
	for _, field := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
		if _, present := request[field]; present {
			t.Fatalf("request must not impose a token ceiling: %s", field)
		}
	}
	if request["prompt_cache_key"] != "group-a" || request["safety_identifier"] != "participant-1" {
		t.Fatal("supported provider options were dropped")
	}
	if _, ok := request["response_format"]; ok {
		t.Fatal("model JSON mode must not be forced")
	}
	data, _ := json.Marshal(log.events)
	if strings.Contains(string(data), "private-test-key") || strings.Contains(string(data), "Bearer") {
		t.Fatal("credential logged")
	}
	if !strings.Contains(string(data), "llm.start") || !strings.Contains(string(data), "llm.response") || !strings.Contains(string(data), "llm.finish") || !strings.Contains(string(data), "mq") {
		t.Fatal("missing diagnostic events")
	}
}

func TestMetaReasoningEffortPrecedence(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "private-test-key")
	for _, tc := range []struct {
		name, model, explicit, want string
	}{
		{"explicit", "muse-wonder-1.0", "medium", "medium"},
		{"model", "muse-wonder-1.0", "", "high"},
		{"default", "muse-spark-1.3-contributor", "", "low"},
		{"empty-model-setting", "empty", "", "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire map[string]any
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				sent, _ = wire["reasoning_effort"].(string)
				_, _ = w.Write([]byte(`{"model":"meta","choices":[{"finish_reason":"stop","message":{"content":"answer"}}]}`))
			}))
			defer server.Close()
			cfg := metaTestConfig(server.URL)
			cfg.ReasoningEffort = "low"
			cfg.ModelReasoningEffort = map[string]string{"muse-wonder-1.0": "high", "empty": ""}
			client, err := NewMetaClient(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Complete(context.Background(), TextRequest{Model: tc.model, ReasoningEffort: tc.explicit, Messages: []Message{{"role": "user", "content": "test"}}})
			if err != nil || sent != tc.want {
				t.Fatalf("expected effort %q, got %q, error %v", tc.want, sent, err)
			}
		})
	}
}

func TestMetaCredentialFileFormats(t *testing.T) {
	for _, tc := range []struct {
		name, format, content, want, errorText string
	}{
		{"default-raw", "", "\ufeff  fake-raw-key \r\n", "fake-raw-key", ""},
		{"explicit-raw", "raw", "fake-raw-key", "fake-raw-key", ""},
		{"raw-multiline-rejected", "raw", "meta api\nfake-key", "", "single value"},
		{"labelled-single-value", "single_value_line", "\ufeffmeta api\r\nThis is an explanatory line.\r\n#comment\r\n//comment\r\n;comment\r\nAPI_KEY:\r\nfake-standalone-key\r\n", "fake-standalone-key", ""},
		{"ambiguous", "single_value_line", "first-value\nsecond-value", "", "multiple standalone"},
		{"no-value", "single_value_line", "meta api\nExplanatory line\napi_key:\n", "", "no standalone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fake-key.txt")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			client := &MetaClient{Config: MetaConfig{APIKeyFile: path, APIKeyFileFormat: tc.format}}
			got, err := client.key()
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("expected %q failure, got %v", tc.errorText, err)
				}
				if got != "" {
					t.Fatal("failed credential parsing returned a value")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("credential format failed: %v", err)
			}
		})
	}
}

func TestMetaCredentialEnvStillTakesPrecedence(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "fake-env-key")
	client := &MetaClient{Config: MetaConfig{APIKeyEnv: "METATHOUGHT_TEST_KEY", APIKeyFile: "does-not-exist", APIKeyFileFormat: "single_value_line"}}
	got, err := client.key()
	if err != nil || got != "fake-env-key" {
		t.Fatalf("environment precedence changed: %v", err)
	}
}

func TestMetaProviderErrorIncludesExactRedactedMessageAndCode(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "private-test-key")
	log := &metaTestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"The supplied key private-test-key is invalid.","code":"invalid_api_key","type":"authentication_error"}}`))
	}))
	defer server.Close()
	client, _ := NewMetaClient(metaTestConfig(server.URL), log)
	_, err := client.Complete(context.Background(), TextRequest{Model: "meta", Messages: []Message{{"role": "user", "content": "test"}}})
	var p *ProviderError
	if !errors.As(err, &p) || p.ProviderCode != "invalid_api_key" || p.Message != "The supplied key [REDACTED] is invalid." {
		t.Fatalf("provider error message/code not preserved: %v", err)
	}
	if err.Error() != "api_rejection (HTTP 401) [code=invalid_api_key]: The supplied key [REDACTED] is invalid." {
		t.Fatalf("unexpected diagnostic string: %v", err)
	}
	data, _ := json.Marshal(log.events)
	if strings.Contains(string(data), "private-test-key") || strings.Contains(err.Error(), "private-test-key") {
		t.Fatal("credential exposed through provider rejection")
	}
}

func TestMetaRejectionRedactsResponseAndDoesNotRetry(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "private-test-key")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"private-test-key rejected"}`))
	}))
	defer server.Close()
	log := &metaTestLog{}
	client, _ := NewMetaClient(metaTestConfig(server.URL), log)
	_, err := client.Complete(context.Background(), TextRequest{Model: "meta", Messages: []Message{{"role": "user", "content": "test"}}})
	var p *ProviderError
	if !errors.As(err, &p) || p.Kind != "api_rejection" || p.StatusCode != 429 || calls != 1 {
		t.Fatalf("unexpected rejection %v, calls %d", err, calls)
	}
	data, _ := json.Marshal(log.events)
	if strings.Contains(string(data), "private-test-key") || strings.Contains(err.Error(), "private-test-key") {
		t.Fatal("secret was exposed")
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Fatal("rejected response evidence missing")
	}
}

func TestMetaRefusalAndMissingUsage(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "private-test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"meta","choices":[{"finish_reason":"content_filter","message":{"content":null}}]}`))
	}))
	defer server.Close()
	client, _ := NewMetaClient(metaTestConfig(server.URL), nil)
	got, err := client.Complete(context.Background(), TextRequest{Model: "meta", Messages: []Message{{"role": "user", "content": "test"}}})
	if err != nil || got.Refusal == "" || got.Usage.TotalTokens != nil {
		t.Fatalf("refusal lost: %#v %v", got, err)
	}
}

func TestMetaMalformedAndTruncatedOutput(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "private-test-key")
	for _, tc := range []struct {
		body, kind string
	}{
		{`{"model":"meta","choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`, "incomplete_output"},
		{`{"model":"meta","choices":[{"message":{"content":[]}}]}`, "format_error"},
		{`{"choices":[{"message":{"content":"answer"}}]}`, "format_error"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			cfg := metaTestConfig(server.URL)
			client, _ := NewMetaClient(cfg, nil)
			_, err := client.Complete(context.Background(), TextRequest{Model: "meta", Messages: []Message{{"role": "user", "content": "test"}}})
			var p *ProviderError
			if !errors.As(err, &p) || p.Kind != tc.kind {
				t.Fatalf("expected %s, got %v", tc.kind, err)
			}
		})
	}
}

func TestMetaRefusesCredentialRedirect(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "private-test-key")
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client, _ := NewMetaClient(metaTestConfig(source.URL), nil)
	_, err := client.Complete(context.Background(), TextRequest{Model: "meta", Messages: []Message{{"role": "user", "content": "test"}}})
	if err == nil || redirected {
		t.Fatal("credential-bearing request followed a redirect")
	}
}

func TestMetaCancellation(t *testing.T) {
	t.Setenv("METATHOUGHT_TEST_KEY", "private-test-key")
	client, _ := NewMetaClient(metaTestConfig("http://127.0.0.1:1"), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Complete(ctx, TextRequest{Model: "meta", Messages: []Message{{"role": "user", "content": "test"}}})
	var p *ProviderError
	if !errors.As(err, &p) || p.Kind != "transport_error" {
		t.Fatalf("cancellation: %v", err)
	}
}
