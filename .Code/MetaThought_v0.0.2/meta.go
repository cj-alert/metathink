package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
)

type EventSink interface {
	Log(scope, event string, fields map[string]any)
}

type Message = map[string]any

type TextRequest struct {
	Model            string
	Messages         []Message
	ReasoningEffort  string
	Scope            string
	PromptCacheKey   *string
	SafetyIdentifier *string
}

// Missing provider counters remain null. A missing measurement is not zero.
type Usage struct {
	InputTokens         *int64             `json:"input_tokens"`
	InputTokensDetails  UsageInputDetails  `json:"input_tokens_details"`
	OutputTokens        *int64             `json:"output_tokens"`
	OutputTokensDetails UsageOutputDetails `json:"output_tokens_details"`
	TotalTokens         *int64             `json:"total_tokens"`
}

type UsageInputDetails struct {
	CacheWriteTokens *int64 `json:"cache_write_tokens"`
	CachedTokens     *int64 `json:"cached_tokens"`
}

type UsageOutputDetails struct {
	ReasoningTokens *int64 `json:"reasoning_tokens"`
}

type TextResponse struct {
	Text, Refusal, Model string
	Usage                Usage
	Raw                  json.RawMessage
}

type TextClient interface {
	Complete(context.Context, TextRequest) (TextResponse, error)
}

type MetaConfig struct {
	BaseURL              string            `json:"base_url"`
	APIKeyEnv            string            `json:"api_key_env"`
	APIKeyFile           string            `json:"api_key_file"`
	APIKeyFileFormat     string            `json:"api_key_file_format,omitempty"`
	ReasoningEffort      string            `json:"reasoning_effort"`
	ModelReasoningEffort map[string]string `json:"model_reasoning_effort,omitempty"`
}

type MetaClient struct {
	Config MetaConfig
	HTTP   *http.Client
	Log    EventSink
}

type ProviderError struct {
	Kind         string
	StatusCode   int
	ProviderCode string
	Message      string
}

func (e *ProviderError) Error() string {
	code := ""
	if e.ProviderCode != "" {
		code = " [code=" + e.ProviderCode + "]"
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s (HTTP %d)%s: %s", e.Kind, e.StatusCode, code, e.Message)
	}
	return e.Kind + code + ": " + e.Message
}

func NewMetaClient(cfg MetaConfig, log EventSink) (*MetaClient, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("meta base_url must be an HTTP(S) URL without credentials, query, or fragment")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return nil, errors.New("meta base_url requires HTTPS except for local tests")
	}
	if cfg.APIKeyFileFormat != "" && cfg.APIKeyFileFormat != "raw" && cfg.APIKeyFileFormat != "single_value_line" {
		return nil, errors.New("meta api_key_file_format must be raw or single_value_line")
	}
	return &MetaClient{Config: cfg, HTTP: &http.Client{
		// Never forward a credential to a redirected host, or silently replay a call.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, Log: log}, nil
}

func (m *MetaClient) key() (string, error) {
	key := ""
	if m.Config.APIKeyEnv != "" {
		key = strings.TrimSpace(os.Getenv(m.Config.APIKeyEnv))
	}
	if key == "" && m.Config.APIKeyFile != "" {
		value, err := os.ReadFile(m.Config.APIKeyFile)
		if err != nil {
			return "", errors.New("cannot read configured Meta credential file")
		}
		key, err = parseMetaKeyFile(string(value), m.Config.APIKeyFileFormat)
		if err != nil {
			return "", err
		}
	}
	if key == "" {
		return "", errors.New("Meta credential is missing")
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", errors.New("Meta credential must be a single value")
	}
	return key, nil
}

func parseMetaKeyFile(contents, format string) (string, error) {
	contents = strings.TrimPrefix(contents, "\ufeff")
	switch format {
	case "", "raw":
		return strings.TrimSpace(contents), nil
	case "single_value_line":
		candidate := ""
		for _, line := range strings.Split(contents, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.IndexFunc(line, unicode.IsSpace) >= 0 ||
				strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") || strings.HasPrefix(line, ";") {
				continue
			}
			label := strings.TrimSuffix(strings.ToLower(line), ":")
			switch label {
			case "meta", "api", "key", "token", "credential", "credentials", "apikey", "api_key", "api-key", "meta_api", "meta.api":
				continue
			}
			if candidate != "" {
				return "", errors.New("Meta credential file contains multiple standalone value lines")
			}
			candidate = line
		}
		if candidate == "" {
			return "", errors.New("Meta credential file contains no standalone value line")
		}
		return candidate, nil
	default:
		return "", errors.New("unsupported Meta credential file format")
	}
}

// The provider's decoded message and code are retained, with credential values
// redacted before the error can reach a caller or any diagnostic sink.
func metaRejection(status int, body []byte, key string) *ProviderError {
	result := &ProviderError{Kind: "api_rejection", StatusCode: status, Message: "Meta rejected the request; see redacted diagnostic response"}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return result
	}
	fields := envelope
	if raw, exists := envelope["error"]; exists {
		var message string
		if json.Unmarshal(raw, &message) == nil {
			if message != "" {
				result.Message = strings.ReplaceAll(message, key, "[REDACTED]")
			}
			return result
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil && nested != nil {
			fields = nested
		}
	}
	var message string
	if json.Unmarshal(fields["message"], &message) == nil && message != "" {
		result.Message = strings.ReplaceAll(message, key, "[REDACTED]")
	}
	var code any
	decoder := json.NewDecoder(bytes.NewReader(fields["code"]))
	decoder.UseNumber()
	if decoder.Decode(&code) == nil {
		switch value := code.(type) {
		case string:
			result.ProviderCode = strings.ReplaceAll(value, key, "[REDACTED]")
		case json.Number:
			result.ProviderCode = value.String()
		}
	}
	return result
}

func (m *MetaClient) Complete(ctx context.Context, in TextRequest) (out TextResponse, resultErr error) {
	if strings.TrimSpace(in.Model) == "" || len(in.Messages) == 0 {
		return out, &ProviderError{Kind: "invalid_request", Message: "model and messages are required"}
	}
	key, err := m.key()
	if err != nil {
		return out, &ProviderError{Kind: "configuration_error", Message: err.Error()}
	}
	effort := in.ReasoningEffort
	if effort == "" {
		effort = m.Config.ModelReasoningEffort[in.Model]
	}
	if effort == "" {
		effort = m.Config.ReasoningEffort
	}
	payload := map[string]any{"model": in.Model, "messages": in.Messages}
	if effort != "" {
		payload["reasoning_effort"] = effort
	}
	if in.PromptCacheKey != nil {
		payload["prompt_cache_key"] = *in.PromptCacheKey
	}
	if in.SafetyIdentifier != nil {
		if len([]rune(*in.SafetyIdentifier)) > 64 {
			return out, &ProviderError{Kind: "invalid_request", Message: "safety_identifier exceeds 64 characters"}
		}
		payload["safety_identifier"] = *in.SafetyIdentifier
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return out, &ProviderError{Kind: "invalid_request", Message: "request is not JSON encodable"}
	}
	scope := in.Scope
	if scope == "" {
		scope = "hive"
	}
	start := time.Now()
	log := func(event string, fields map[string]any) {
		if m.Log != nil {
			m.Log.Log(scope, event, fields)
		}
	}
	log("llm.start", map[string]any{"provider": "meta", "model": in.Model, "request": strings.ReplaceAll(string(data), key, "[REDACTED]")})
	defer func() {
		fields := map[string]any{"provider": "meta", "model": in.Model, "duration_ms": time.Since(start).Milliseconds()}
		if resultErr != nil {
			fields["outcome"] = "error"
			fields["error"] = strings.ReplaceAll(resultErr.Error(), key, "[REDACTED]")
			var p *ProviderError
			if errors.As(resultErr, &p) {
				fields["kind"], fields["status_code"] = p.Kind, p.StatusCode
			}
		} else {
			fields["outcome"], fields["model"], fields["usage"] = "answered", out.Model, out.Usage
			fields["reply"] = strings.ReplaceAll(out.Text, key, "[REDACTED]")
			fields["refusal"] = strings.ReplaceAll(out.Refusal, key, "[REDACTED]")
			if out.Refusal != "" {
				fields["outcome"] = "refusal"
			}
		}
		log("llm.finish", fields)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.Config.BaseURL, "/")+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return out, &ProviderError{Kind: "configuration_error", Message: "cannot construct Meta request"}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return out, &ProviderError{Kind: "transport_error", Message: ctx.Err().Error()}
		}
		// Do not return transport errors containing request URLs or header values.
		return out, &ProviderError{Kind: "transport_error", Message: "Meta HTTP request failed"}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, &ProviderError{Kind: "transport_error", Message: "Meta response could not be read"}
	}
	// Retain a redacted response for debugging, including rejected responses.
	log("llm.response", map[string]any{"provider": "meta", "model": in.Model, "status_code": resp.StatusCode, "body": strings.ReplaceAll(string(body), key, "[REDACTED]")})
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, metaRejection(resp.StatusCode, body, key)
	}
	var wire struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content json.RawMessage `json:"content"`
				Refusal string          `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens            *int64             `json:"prompt_tokens"`
			CompletionTokens        *int64             `json:"completion_tokens"`
			TotalTokens             *int64             `json:"total_tokens"`
			PromptTokensDetails     UsageInputDetails  `json:"prompt_tokens_details"`
			CompletionTokensDetails UsageOutputDetails `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &wire) != nil || len(wire.Choices) != 1 || wire.Model == "" {
		return out, &ProviderError{Kind: "format_error", Message: "Meta response lacks one completion or its actual model"}
	}
	choice := wire.Choices[0]
	if choice.FinishReason == "length" {
		return out, &ProviderError{Kind: "incomplete_output", Message: "Meta completion reached its token limit"}
	}
	if choice.FinishReason == "content_filter" && choice.Message.Refusal == "" {
		choice.Message.Refusal = "provider_content_filter"
	}
	if len(choice.Message.Content) > 0 && string(choice.Message.Content) != "null" {
		if json.Unmarshal(choice.Message.Content, &out.Text) != nil {
			return out, &ProviderError{Kind: "format_error", Message: "Meta completion content is not text"}
		}
	}
	out.Refusal, out.Model = choice.Message.Refusal, wire.Model
	if out.Refusal == "" && strings.TrimSpace(out.Text) == "" {
		return out, &ProviderError{Kind: "format_error", Message: "Meta completion contains neither text nor refusal"}
	}
	out.Usage = Usage{InputTokens: wire.Usage.PromptTokens, OutputTokens: wire.Usage.CompletionTokens, TotalTokens: wire.Usage.TotalTokens,
		InputTokensDetails: wire.Usage.PromptTokensDetails, OutputTokensDetails: wire.Usage.CompletionTokensDetails}
	out.Raw = json.RawMessage(strings.ReplaceAll(string(body), key, "[REDACTED]"))
	return out, nil
}
