package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// The public field names follow OpenAI's create-decision contract. Raw input
// and instructions retain every documented content variant without an SDK.
type DecisionRequest struct {
	Model              string                `json:"model"`
	Input              json.RawMessage       `json:"input"`
	Questions          []DecisionQuestion    `json:"questions"`
	PromptCacheKey     *string               `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions *DecisionCacheOptions `json:"prompt_cache_options,omitempty"`
	SafetyIdentifier   *string               `json:"safety_identifier,omitempty"`
	// Internal opt-in grading; excluded from the public Decisions JSON contract.
	LocalScoring string `json:"-"`
	ContextKind  string `json:"-"`
}

type DecisionCacheOptions struct {
	Mode *string `json:"mode,omitempty"`
	TTL  *string `json:"ttl,omitempty"`
}

type DecisionQuestion struct {
	Type         string           `json:"type"`
	Name         *string          `json:"name,omitempty"`
	Instructions json.RawMessage  `json:"instructions"`
	Choices      []DecisionChoice `json:"choices,omitempty"`
	Levels       []DecisionLevel  `json:"levels,omitempty"`
}

type DecisionChoice struct {
	Value       any     `json:"value"`
	Description *string `json:"description,omitempty"`
}

type DecisionLevel struct {
	Label       string  `json:"label"`
	Description *string `json:"description,omitempty"`
}

type Decision struct {
	Answers []DecisionAnswer `json:"answers"`
	Model   string           `json:"model"`
	Usage   Usage            `json:"usage"`
}

type DecisionAnswer struct {
	Type            string                `json:"type"`
	Name            *string               `json:"name"`
	Probability     *float64              `json:"probability,omitempty"`
	Choice          any                   `json:"choice,omitempty"`
	Confidence      *float64              `json:"confidence,omitempty"`
	Score           *float64              `json:"score,omitempty"`
	Probabilities   []DecisionProbability `json:"probabilities,omitempty"`
	LocalAssessment *ScoredText           `json:"-"`
}

type DecisionProbability struct {
	Value       any     `json:"value"`
	Label       *string `json:"label,omitempty"`
	Probability float64 `json:"probability"`
}

type DecisionPrompts struct {
	System      string `json:"system"`
	InputPrefix string `json:"input_prefix"`
	Predicate   string `json:"predicate"`
	Choice      string `json:"choice"`
	Score       string `json:"score"`
	LocalScores string `json:"local_scores,omitempty"`
}

type DecisionClient interface {
	Decide(context.Context, DecisionRequest) (Decision, error)
}

type MetaDecisions struct {
	Client  TextClient
	Prompts DecisionPrompts
	Log     EventSink
	Scope   string
}

func NewMetaDecisions(client TextClient, prompts DecisionPrompts, log EventSink) *MetaDecisions {
	return &MetaDecisions{Client: client, Prompts: prompts, Log: log, Scope: "hive"}
}

func (r *DecisionRequest) UnmarshalJSON(data []byte) error {
	type plain DecisionRequest
	var p plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&p); err != nil {
		return err
	}
	*r = DecisionRequest(p)
	return nil
}

func (q *DecisionQuestion) UnmarshalJSON(data []byte) error {
	type plain DecisionQuestion
	var p plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if name, present := fields["name"]; present && bytes.Equal(bytes.TrimSpace(name), []byte("null")) {
		return errors.New("question name may be omitted but cannot be null")
	}
	_, choicesPresent := fields["choices"]
	_, levelsPresent := fields["levels"]
	if (p.Type != "choice" && choicesPresent) || (p.Type != "score" && levelsPresent) {
		return errors.New("question contains fields for another answer type")
	}
	for _, optionKey := range []string{"choices", "levels"} {
		if options, present := fields[optionKey]; present {
			var rows []map[string]json.RawMessage
			if err := json.Unmarshal(options, &rows); err != nil {
				return err
			}
			for _, row := range rows {
				if description, exists := row["description"]; exists && bytes.Equal(bytes.TrimSpace(description), []byte("null")) {
					return errors.New("option description may be omitted but cannot be null")
				}
			}
		}
	}
	*q = DecisionQuestion(p)
	return nil
}

func (o *DecisionCacheOptions) UnmarshalJSON(data []byte) error {
	type plain DecisionCacheOptions
	var p plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if ttl, present := fields["ttl"]; present && bytes.Equal(bytes.TrimSpace(ttl), []byte("null")) {
		return errors.New("cache ttl may be omitted but cannot be null")
	}
	*o = DecisionCacheOptions(p)
	return nil
}

func decisionError(kind, message string) error {
	return &ProviderError{Kind: kind, Message: message}
}

func (m *MetaDecisions) Decide(ctx context.Context, request DecisionRequest) (result Decision, resultErr error) {
	started := time.Now()
	scope := m.Scope
	if scope == "" {
		scope = "hive"
	}
	log := func(event string, fields map[string]any) {
		if m.Log != nil {
			if _, exists := fields["decision_type"]; !exists {
				if class, present := fields["class"]; present {
					fields["decision_type"] = class
				}
			}
			if _, exists := fields["model"]; !exists {
				fields["model"] = request.Model
			}
			if _, exists := fields["provider"]; !exists {
				fields["provider"] = "meta"
			}
			m.Log.Log(scope, event, fields)
		}
	}
	log("decision.start", map[string]any{"provider": "meta", "model": request.Model, "questions": len(request.Questions)})
	defer func() {
		fields := map[string]any{"provider": "meta", "model": request.Model, "questions": len(request.Questions), "answers": len(result.Answers), "duration_ms": time.Since(started).Milliseconds()}
		if resultErr != nil {
			fields["outcome"], fields["error"] = "error", resultErr.Error()
			var p *ProviderError
			if errors.As(resultErr, &p) {
				fields["kind"] = p.Kind
			}
		} else {
			fields["outcome"] = "completed"
		}
		log("decision.finish", fields)
	}()
	input, questions, err := prepareDecision(request)
	if err != nil {
		return result, err
	}
	if m.Client == nil || strings.TrimSpace(m.Prompts.System) == "" || strings.TrimSpace(m.Prompts.InputPrefix) == "" {
		return result, decisionError("configuration_error", "decision client and configured prompts are required")
	}
	localScoring := request.LocalScoring
	if localScoring == "" {
		localScoring = m.Prompts.LocalScores
	}
	result.Answers = make([]DecisionAnswer, 0, len(request.Questions))
	for i, question := range request.Questions {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		questionContent, err := m.questionPrompt(question, questions[i].Parts)
		if err != nil {
			return result, err
		}
		content := []any{map[string]any{"type": "text", "text": m.Prompts.InputPrefix}}
		content = append(content, input...)
		content = append(content, questionContent...)
		log("decision.question.start", map[string]any{"provider": "meta", "model": request.Model, "index": i, "name": question.Name, "class": question.Type})
		system := m.Prompts.System
		if localScoring != "" {
			system += "\n\n" + localScoring
		}
		response, err := m.Client.Complete(ctx, TextRequest{Model: request.Model, Scope: scope, ContextKind: request.ContextKind, PromptCacheKey: request.PromptCacheKey, SafetyIdentifier: request.SafetyIdentifier, Messages: []Message{
			{"role": "developer", "content": system},
			{"role": "user", "content": content},
		}})
		if err != nil {
			outcome := "error"
			var providerError *ProviderError
			if errors.As(err, &providerError) && providerError.Kind != "" {
				outcome = providerError.Kind
			}
			log("decision.question.finish", map[string]any{"index": i, "name": question.Name, "class": question.Type, "outcome": outcome, "error": err.Error()})
			return result, err
		}
		var answer DecisionAnswer
		if response.Refusal != "" {
			answer = DecisionAnswer{Type: "refusal", Name: question.Name}
		} else {
			text := response.Text
			var local *ScoredText
			if localScoring != "" && strings.TrimSpace(text) != "# REFUSAL" {
				scored, scoreErr := ParseScoredText(text)
				if scoreErr != nil {
					log("decision.question.finish", map[string]any{"index": i, "name": question.Name, "class": question.Type, "outcome": "format_error", "error": scoreErr.Error()})
					return result, decisionError("format_error", fmt.Sprintf("question %d local assessment: %s", i, scoreErr))
				}
				text, local = scored.Body, &scored
			}
			answer, err = parseDecisionReply(question, text)
			answer.LocalAssessment = local
			if err != nil {
				log("decision.question.finish", map[string]any{"index": i, "name": question.Name, "class": question.Type, "outcome": "format_error", "error": err.Error()})
				return result, decisionError("format_error", fmt.Sprintf("question %d: %s", i, err.Error()))
			}
		}
		result.Answers = append(result.Answers, answer)
		if i == 0 {
			result.Usage = response.Usage
		} else {
			result.Usage = sumUsage(result.Usage, response.Usage)
		}
		result.Model = response.Model
		outcome := "answered"
		if answer.Type == "refusal" {
			outcome = "refusal"
		}
		log("decision.question.finish", map[string]any{"index": i, "name": question.Name, "class": question.Type, "outcome": outcome, "answer": answer, "local_assessment": answer.LocalAssessment, "probability_source": "meta_self_report", "score_source": "weighted_distribution", "usage": response.Usage})
	}
	return result, nil
}

type preparedQuestion struct {
	Parts []any
}

func (m *MetaDecisions) questionPrompt(q DecisionQuestion, instructions []any) ([]any, error) {
	var source string
	var options strings.Builder
	switch q.Type {
	case "predicate":
		source = m.Prompts.Predicate
	case "choice":
		source = m.Prompts.Choice
		for i, c := range q.Choices {
			value, _ := json.Marshal(c.Value)
			fmt.Fprintf(&options, "%d: %s", i+1, value)
			if c.Description != nil {
				options.WriteString(" — " + *c.Description)
			}
			options.WriteByte('\n')
		}
	case "score":
		source = m.Prompts.Score
		for i, level := range q.Levels {
			fmt.Fprintf(&options, "%d: %s", i+1, level.Label)
			if level.Description != nil {
				options.WriteString(" — " + *level.Description)
			}
			options.WriteByte('\n')
		}
	}
	if strings.TrimSpace(source) == "" {
		return nil, decisionError("configuration_error", "missing configured "+q.Type+" decision prompt")
	}
	tmpl, err := template.New(q.Type).Option("missingkey=error").Parse(source)
	if err != nil {
		return nil, decisionError("configuration_error", "invalid decision prompt template")
	}
	var buf bytes.Buffer
	const questionSlot = "\x00METATHOUGHT_QUESTION\x00"
	if err := tmpl.Execute(&buf, map[string]string{"Question": questionSlot, "Options": options.String()}); err != nil {
		return nil, decisionError("configuration_error", "decision prompt template failed")
	}
	if strings.Count(buf.String(), questionSlot) != 1 {
		return nil, decisionError("configuration_error", "decision prompt must include Question exactly once")
	}
	before, after, _ := strings.Cut(buf.String(), questionSlot)
	var out []any
	if before != "" {
		out = append(out, map[string]any{"type": "text", "text": before})
	}
	out = append(out, instructions...)
	if after != "" {
		out = append(out, map[string]any{"type": "text", "text": after})
	}
	return out, nil
}

func prepareDecision(r DecisionRequest) ([]any, []preparedQuestion, error) {
	if strings.TrimSpace(r.Model) == "" {
		return nil, nil, decisionError("invalid_request", "model is required")
	}
	if len(r.Questions) < 1 || len(r.Questions) > 400 {
		return nil, nil, decisionError("invalid_request", "questions must contain 1 to 400 items")
	}
	if r.PromptCacheOptions != nil {
		o := r.PromptCacheOptions
		if (o.Mode != nil && *o.Mode != "implicit" && *o.Mode != "explicit") || (o.TTL != nil && *o.TTL != "30m") {
			return nil, nil, decisionError("invalid_request", "invalid prompt_cache_options")
		}
	}
	if r.PromptCacheOptions != nil {
		return nil, nil, decisionError("unsupported_feature", "Meta adapter cannot guarantee native Decisions cache controls")
	}
	if r.SafetyIdentifier != nil && len([]rune(*r.SafetyIdentifier)) > 64 {
		return nil, nil, decisionError("invalid_request", "safety_identifier exceeds 64 characters")
	}
	input, err := decisionInput(r.Input)
	if err != nil {
		return nil, nil, err
	}
	prepared := make([]preparedQuestion, 0, len(r.Questions))
	names := map[string]bool{}
	for i, q := range r.Questions {
		if q.Name != nil {
			if names[*q.Name] {
				return nil, nil, decisionError("invalid_request", "question names must be unique when provided")
			}
			names[*q.Name] = true
		}
		switch q.Type {
		case "predicate":
			if q.Choices != nil || q.Levels != nil {
				return nil, nil, decisionError("invalid_request", "predicate cannot contain choices or levels")
			}
		case "choice":
			if len(q.Choices) < 2 || len(q.Choices) > 255 || q.Levels != nil {
				return nil, nil, decisionError("invalid_request", "choice requires 2 to 255 choices and no levels")
			}
			values := map[string]bool{}
			for _, c := range q.Choices {
				switch c.Value.(type) {
				case bool, string:
				default:
					return nil, nil, decisionError("invalid_request", "choice values must be strings or booleans")
				}
				value, _ := json.Marshal(c.Value)
				key := string(value)
				if values[key] {
					return nil, nil, decisionError("invalid_request", "choice values must be unique")
				}
				values[key] = true
			}
		case "score":
			if len(q.Levels) == 0 || q.Choices != nil {
				return nil, nil, decisionError("invalid_request", "score requires ordered levels and no choices")
			}
			labels := map[string]bool{}
			for _, l := range q.Levels {
				if labels[l.Label] {
					return nil, nil, decisionError("invalid_request", "score labels must be unique")
				}
				labels[l.Label] = true
			}
		default:
			return nil, nil, decisionError("invalid_request", "unsupported question type")
		}
		parts, err := decisionContent(q.Instructions, "question")
		if err != nil {
			return nil, nil, decisionErrorKind(err, fmt.Sprintf("question %d instructions", i))
		}
		prepared = append(prepared, preparedQuestion{Parts: parts})
	}
	return input, prepared, nil
}

func decisionErrorKind(err error, prefix string) error {
	var p *ProviderError
	if errors.As(err, &p) {
		return decisionError(p.Kind, prefix+": "+p.Message)
	}
	return decisionError("invalid_request", prefix+": "+err.Error())
}

func decodeDecisionJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var v any
	if err := decoder.Decode(&v); err != nil {
		return nil, decisionError("invalid_request", "invalid JSON content")
	}
	if decoder.More() {
		return nil, decisionError("invalid_request", "extra JSON content")
	}
	return v, nil
}

func allowedFields(obj map[string]any, fields ...string) error {
	for key := range obj {
		found := false
		for _, f := range fields {
			if key == f {
				found = true
				break
			}
		}
		if !found {
			return decisionError("invalid_request", "undocumented field: "+key)
		}
	}
	return nil
}

func requiredString(obj map[string]any, key string) (string, error) {
	s, ok := obj[key].(string)
	if !ok {
		return "", decisionError("invalid_request", key+" must be a string")
	}
	return s, nil
}

func decisionInput(raw json.RawMessage) ([]any, error) {
	v, err := decodeDecisionJSON(raw)
	if err != nil {
		return nil, err
	}
	if text, ok := v.(string); ok {
		return []any{map[string]any{"type": "text", "text": text}}, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, decisionError("invalid_request", "input must be text or an ordered array")
	}
	calls := map[string]string{}
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, decisionError("invalid_request", "input item must be an object")
		}
		if obj["type"] == "function_call" || obj["type"] == "custom_tool_call" {
			id, err := requiredString(obj, "call_id")
			if err != nil || id == "" {
				return nil, decisionError("invalid_request", "tool call requires call_id")
			}
			if _, exists := calls[id]; exists {
				return nil, decisionError("invalid_request", "duplicate tool call_id")
			}
			calls[id] = obj["type"].(string)
		}
	}
	var out []any
	for _, item := range items {
		obj := item.(map[string]any)
		if value, exists := obj["type"]; exists {
			if s, ok := value.(string); !ok || s == "" {
				return nil, decisionError("invalid_request", "input item type must be a string")
			}
		}
		itemType, _ := obj["type"].(string)
		if itemType == "" {
			itemType = "message"
		}
		copy := make(map[string]any, len(obj))
		for k, v := range obj {
			copy[k] = v
		}
		var content []any
		switch itemType {
		case "message":
			if err := allowedFields(obj, "type", "role", "content", "phase"); err != nil {
				return nil, err
			}
			role, err := requiredString(obj, "role")
			if err != nil {
				return nil, err
			}
			if role != "user" && role != "developer" && role != "system" && role != "assistant" {
				return nil, decisionError("invalid_request", "invalid input message role")
			}
			if phase, exists := obj["phase"]; exists {
				if role != "assistant" || (phase != nil && phase != "commentary" && phase != "final_answer") {
					return nil, decisionError("invalid_request", "phase is only valid on assistant messages")
				}
			}
			data, _ := json.Marshal(obj["content"])
			content, err = decisionContent(data, role)
			if err != nil {
				return nil, err
			}
			delete(copy, "content")
		case "function_call", "custom_tool_call":
			payloadName := "arguments"
			if itemType == "custom_tool_call" {
				payloadName = "input"
			}
			if err := allowedFields(obj, "type", "call_id", "name", payloadName, "id", "namespace", "status"); err != nil {
				return nil, err
			}
			for _, key := range []string{"call_id", "name", payloadName} {
				if _, err := requiredString(obj, key); err != nil {
					return nil, err
				}
			}
			if err := validateToolMetadata(obj); err != nil {
				return nil, err
			}
		case "function_call_output", "custom_tool_call_output":
			if err := allowedFields(obj, "type", "call_id", "output", "id", "status"); err != nil {
				return nil, err
			}
			id, err := requiredString(obj, "call_id")
			if err != nil || calls[id] != strings.TrimSuffix(itemType, "_output") {
				return nil, decisionError("invalid_request", "tool output must identify its matching call")
			}
			if err := validateToolMetadata(obj); err != nil {
				return nil, err
			}
			data, _ := json.Marshal(obj["output"])
			content, err = decisionContent(data, "tool")
			if err != nil {
				return nil, err
			}
			delete(copy, "output")
		default:
			return nil, decisionError("invalid_request", "unsupported input item type")
		}
		meta, _ := json.Marshal(copy)
		out = append(out, map[string]any{"type": "text", "text": string(meta)})
		out = append(out, content...)
	}
	return out, nil
}

func validateToolMetadata(obj map[string]any) error {
	for _, key := range []string{"id", "namespace"} {
		if v, exists := obj[key]; exists && v != nil {
			if _, ok := v.(string); !ok {
				return decisionError("invalid_request", key+" must be a string or null")
			}
		}
	}
	if v, exists := obj["status"]; exists && v != nil && v != "in_progress" && v != "completed" && v != "incomplete" {
		return decisionError("invalid_request", "invalid tool status")
	}
	return nil
}

func decisionContent(raw json.RawMessage, role string) ([]any, error) {
	v, err := decodeDecisionJSON(raw)
	if err != nil {
		return nil, err
	}
	if text, ok := v.(string); ok {
		return []any{map[string]any{"type": "text", "text": text}}, nil
	}
	parts, ok := v.([]any)
	if !ok {
		return nil, decisionError("invalid_request", "content must be text or an ordered parts array")
	}
	var out []any
	for _, part := range parts {
		obj, ok := part.(map[string]any)
		if !ok {
			return nil, decisionError("invalid_request", "content part must be an object")
		}
		t, err := requiredString(obj, "type")
		if err != nil {
			return nil, err
		}
		if breakpoint, exists := obj["prompt_cache_breakpoint"]; exists {
			if role == "question" || role == "assistant" || (t != "input_text" && t != "input_image") {
				return nil, decisionError("invalid_request", "cache breakpoints are not allowed on this content part")
			}
			if breakpoint == nil {
				// The documented null value requests no explicit breakpoint.
			} else {
				b, ok := breakpoint.(map[string]any)
				if !ok || len(b) != 1 || b["mode"] != "explicit" {
					return nil, decisionError("invalid_request", "invalid prompt_cache_breakpoint")
				}
				return nil, decisionError("unsupported_feature", "Meta adapter cannot honor native Decisions cache breakpoints")
			}
		}
		switch t {
		case "input_text":
			if role == "assistant" {
				return nil, decisionError("invalid_request", "assistant content requires output_text")
			}
			if err := allowedFields(obj, "type", "text", "prompt_cache_breakpoint"); err != nil {
				return nil, err
			}
			text, err := requiredString(obj, "text")
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"type": "text", "text": text})
		case "output_text", "refusal":
			if role != "assistant" {
				return nil, decisionError("invalid_request", t+" requires assistant content")
			}
			key := "text"
			if t == "refusal" {
				key = "refusal"
			}
			if err := allowedFields(obj, "type", key); err != nil {
				return nil, err
			}
			text, err := requiredString(obj, key)
			if err != nil {
				return nil, err
			}
			if t == "refusal" {
				data, _ := json.Marshal(obj)
				text = string(data)
			}
			out = append(out, map[string]any{"type": "text", "text": text})
		case "input_image":
			if role != "user" && role != "question" && role != "tool" {
				return nil, decisionError("invalid_request", "images are not allowed in this content role")
			}
			if err := allowedFields(obj, "type", "image_url", "detail", "prompt_cache_breakpoint"); err != nil {
				return nil, err
			}
			imageURL, err := requiredString(obj, "image_url")
			if err != nil {
				return nil, err
			}
			u, err := url.Parse(imageURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https" && !strings.HasPrefix(imageURL, "data:image/")) {
				return nil, decisionError("invalid_request", "image_url must be an HTTP(S) URL or image data URL")
			}
			image := map[string]any{"url": imageURL}
			if detail, exists := obj["detail"]; exists && detail != nil {
				if detail != "low" && detail != "high" && detail != "auto" && detail != "original" {
					return nil, decisionError("invalid_request", "invalid image detail")
				}
				image["detail"] = detail
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": image})
		case "input_json":
			if role != "user" && role != "question" {
				return nil, decisionError("invalid_request", "JSON is not allowed in this content role")
			}
			if err := allowedFields(obj, "type", "data"); err != nil {
				return nil, err
			}
			data := obj["data"]
			switch value := data.(type) {
			case map[string]any:
			case []any:
				for _, item := range value {
					if _, ok := item.(map[string]any); !ok {
						return nil, decisionError("invalid_request", "input_json array items must be objects")
					}
				}
			default:
				return nil, decisionError("invalid_request", "input_json data must be an object or array of objects")
			}
			count := 0
			if err := validateJSONDepth(data, 0, &count); err != nil {
				return nil, err
			}
			encoded, _ := json.Marshal(data)
			if len(encoded) > 256*1024 {
				return nil, decisionError("invalid_request", "input_json exceeds 256 KiB")
			}
			out = append(out, map[string]any{"type": "text", "text": string(encoded)})
		default:
			return nil, decisionError("invalid_request", "unsupported content part type")
		}
	}
	return out, nil
}

func validateJSONDepth(v any, depth int, count *int) error {
	*count++
	if *count > 10000 {
		return decisionError("invalid_request", "input_json exceeds 10000 values")
	}
	switch value := v.(type) {
	case map[string]any:
		if depth >= 32 {
			return decisionError("invalid_request", "input_json exceeds 32 container levels")
		}
		for _, child := range value {
			if err := validateJSONDepth(child, depth+1, count); err != nil {
				return err
			}
		}
	case []any:
		if depth >= 32 {
			return decisionError("invalid_request", "input_json exceeds 32 container levels")
		}
		for _, child := range value {
			if err := validateJSONDepth(child, depth+1, count); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseDecisionReply(question DecisionQuestion, reply string) (DecisionAnswer, error) {
	if text := strings.TrimSpace(reply); text != "" && !strings.HasPrefix(text, "#") {
		normalized, err := decisionSectionsFromValues(question, text)
		if err != nil {
			return DecisionAnswer{}, err
		}
		reply = normalized
	}
	answer := DecisionAnswer{Type: question.Type, Name: question.Name}
	sections := map[string]string{}
	current := ""
	for _, line := range strings.Split(strings.ReplaceAll(reply, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			current = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if _, exists := sections[current]; exists {
				return answer, errors.New("duplicate reply section")
			}
			sections[current] = ""
			continue
		}
		if current == "" {
			return answer, errors.New("text before first reply section")
		}
		if sections[current] != "" {
			sections[current] += "\n"
		}
		sections[current] += line
	}
	if _, refused := sections["REFUSAL"]; refused {
		if len(sections) != 1 {
			return answer, errors.New("refusal cannot coexist with answer sections")
		}
		return DecisionAnswer{Type: "refusal", Name: question.Name}, nil
	}
	if question.Type == "predicate" {
		if len(sections) != 1 {
			return answer, errors.New("predicate requires only PROBABILITY")
		}
		value, err := parseProbability(sections["PROBABILITY"])
		if err != nil {
			return answer, err
		}
		answer.Probability = &value
		return answer, nil
	}
	expected := 2
	if question.Type == "choice" {
		expected = 3
	}
	if len(sections) != expected {
		return answer, errors.New("wrong number of answer sections")
	}
	for key := range sections {
		if key != "PROBABILITIES" && key != "CONFIDENCE" && !(key == "CHOICE" && question.Type == "choice") {
			return answer, errors.New("unexpected answer section")
		}
	}
	confidence, err := parseProbability(sections["CONFIDENCE"])
	if err != nil {
		return answer, errors.New("invalid CONFIDENCE")
	}
	answer.Confidence = &confidence
	size := len(question.Levels)
	if question.Type == "choice" {
		size = len(question.Choices)
	}
	probabilities := make([]float64, size)
	seen := make([]bool, size)
	for position, line := range strings.Split(sections["PROBABILITIES"], "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			key, value, found = strconv.Itoa(position+1), line, true
		}
		index, e := strconv.Atoi(strings.TrimSpace(key))
		if !found || e != nil || index < 1 || index > size || seen[index-1] {
			return answer, errors.New("invalid or duplicate probability position")
		}
		p, err := parseProbability(strings.TrimSpace(value))
		if err != nil {
			return answer, err
		}
		seen[index-1], probabilities[index-1] = true, p
	}
	sum := 0.0
	score := 0.0
	for i, p := range probabilities {
		if !seen[i] {
			return answer, errors.New("missing probability position")
		}
		sum += p
		score += float64(i) * p
		item := DecisionProbability{Probability: p}
		if question.Type == "choice" {
			item.Value = question.Choices[i].Value
		} else {
			item.Value, item.Label = i, &question.Levels[i].Label
		}
		answer.Probabilities = append(answer.Probabilities, item)
	}
	if math.Abs(sum-1) > 0.000001 {
		return answer, errors.New("probabilities must sum to 1")
	}
	if question.Type == "choice" {
		index, err := strconv.Atoi(strings.TrimSpace(sections["CHOICE"]))
		if err != nil || index < 1 || index > size {
			return answer, errors.New("CHOICE must be a supplied option position")
		}
		answer.Choice = question.Choices[index-1].Value
	} else {
		answer.Score = &score
	}
	return answer, nil
}

func parseProbability(s string) (float64, error) {
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
		return 0, errors.New("probability must be a finite number from 0 to 1")
	}
	return n, nil
}

func sumUsage(a, b Usage) Usage {
	add := func(x, y *int64) *int64 {
		if x == nil || y == nil {
			return nil
		}
		n := *x + *y
		return &n
	}
	return Usage{InputTokens: add(a.InputTokens, b.InputTokens), OutputTokens: add(a.OutputTokens, b.OutputTokens), TotalTokens: add(a.TotalTokens, b.TotalTokens),
		InputTokensDetails:  UsageInputDetails{CacheWriteTokens: add(a.InputTokensDetails.CacheWriteTokens, b.InputTokensDetails.CacheWriteTokens), CachedTokens: add(a.InputTokensDetails.CachedTokens, b.InputTokensDetails.CachedTokens)},
		OutputTokensDetails: UsageOutputDetails{ReasoningTokens: add(a.OutputTokensDetails.ReasoningTokens, b.OutputTokensDetails.ReasoningTokens)}}
}
