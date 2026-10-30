package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type reassessmentSourceCall struct {
	CallID     string `json:"call_id"`
	PromptKey  string `json:"prompt_key"`
	SubjectKey string `json:"subject_key"`
	LogTable   string `json:"log_table"`
	ActorKind  string `json:"actor_kind"`
	DroneID    int64  `json:"drone_id"`
	InquiryID  int64  `json:"inquiry_id"`
	RequestRaw string `json:"request_raw"`
}

// These are the payload fields supported by MetaClient. Unknown captured fields
// are rejected rather than silently discarded during a prompt-only replay.
type reassessmentRequest struct {
	Model            string    `json:"model"`
	Messages         []Message `json:"messages"`
	ReasoningEffort  string    `json:"reasoning_effort,omitempty"`
	PromptCacheKey   *string   `json:"prompt_cache_key,omitempty"`
	SafetyIdentifier *string   `json:"safety_identifier,omitempty"`
}

type reassessmentInput struct {
	MetaContract    Contract            `json:"meta_contract"`
	ScoringElements []ContractCriterion `json:"scoring_elements"`
	Drone           struct {
		Pair any `json:"pair"`
	} `json:"drone"`
}

// ReassessSaved replays only captured meta-drone assessment requests. Earlier
// original profiles remain in each request exactly as captured; new profiles
// are not substituted into subsequent requests. No source database is opened.
func ReassessSaved(ctx context.Context, c Config, dir, transcriptPath, subject string) error {
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(c.Prompts["assess"]) == "" {
		return errors.New("reassessment requires a subject and configured assess prompt")
	}
	sourcePath, err := filepath.Abs(transcriptPath)
	if err != nil {
		return err
	}
	encoded, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read saved transcript: %w", err)
	}
	var transcript struct {
		Calls []reassessmentSourceCall `json:"calls"`
	}
	if err = json.Unmarshal(encoded, &transcript); err != nil {
		return fmt.Errorf("saved transcript: %w", err)
	}
	var selected []reassessmentSourceCall
	var sourceIDs []string
	for _, call := range transcript.Calls {
		if call.PromptKey == "assess" && call.SubjectKey == subject && call.LogTable == "mq_log" && call.ActorKind == "drone" {
			selected = append(selected, call)
			sourceIDs = append(sourceIDs, call.CallID)
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("saved transcript has no meta-drone assessment calls for subject %q", subject)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		protected := strings.HasPrefix(strings.ToLower(name), "reassess-")
		for _, reserved := range []string{"config.json", "mq_config.json", "comparison.md", c.Files.Debug, c.Files.State, c.Files.Result, c.Files.Contract} {
			protected = protected || strings.EqualFold(name, reserved)
		}
		if protected {
			return fmt.Errorf("reassessment output already exists: %s; choose a fresh folder", name)
		}
	}
	if err = writeReassessmentArtifact(dir, "reassess-config.json", c); err != nil {
		return err
	}
	if err = writeReassessmentArtifact(dir, "reassess-source.json", map[string]any{
		"transcript": sourcePath, "subject": subject, "source_call_ids": sourceIDs,
		"context_policy": "Original captured messages; earlier original profiles retained; replay results do not feed later calls.",
	}); err != nil {
		return err
	}
	logger, err := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		return err
	}
	defer logger.Close()
	// Absence of an effort in a captured request must remain absence. Current
	// defaults cannot silently introduce another experimental change.
	provider := c.Meta
	provider.ReasoningEffort, provider.ModelReasoningEffort = "", nil
	for index, source := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		var request reassessmentRequest
		decoder := json.NewDecoder(strings.NewReader(source.RequestRaw))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return fmt.Errorf("source call %s request: %w", source.CallID, err)
		}
		developerIndex, userIndex := -1, -1
		for n, message := range request.Messages {
			switch message["role"] {
			case "developer":
				if developerIndex != -1 {
					return fmt.Errorf("source call %s has multiple developer messages", source.CallID)
				}
				developerIndex = n
			case "user":
				if userIndex != -1 {
					return fmt.Errorf("source call %s has multiple user messages", source.CallID)
				}
				userIndex = n
			}
		}
		if developerIndex < 0 || userIndex < 0 || strings.TrimSpace(request.Model) == "" {
			return fmt.Errorf("source call %s lacks a model, developer message or user message", source.CallID)
		}
		originalPrompt, developerText := request.Messages[developerIndex]["content"].(string)
		userText, userContentText := request.Messages[userIndex]["content"].(string)
		if !developerText || !userContentText {
			return fmt.Errorf("source call %s requires captured text messages", source.CallID)
		}
		var input reassessmentInput
		if err := json.Unmarshal([]byte(userText), &input); err != nil {
			return fmt.Errorf("source call %s assessment context: %w", source.CallID, err)
		}
		if len(input.ScoringElements) == 0 {
			return fmt.Errorf("source call %s lacks top-level scoring_elements", source.CallID)
		}
		revisedPrompt, err := renderPrompt(c.Prompts["assess"], map[string]any{"Contract": input.MetaContract, "Chromosome": input.Drone.Pair})
		if err != nil {
			return err
		}
		request.Messages[developerIndex]["content"] = revisedPrompt
		prefix := fmt.Sprintf("reassess-%03d", index+1)
		if err = writeReassessmentArtifact(dir, prefix+"-request.json", map[string]any{
			"source_call_id": source.CallID, "subject": subject, "drone_id": source.DroneID,
			"original_developer": originalPrompt, "revised_developer": revisedPrompt,
			"original_request": json.RawMessage(source.RequestRaw), "revised_request": request,
		}); err != nil {
			return err
		}
		log := &metaQueenLog{Sink: logger, Fields: map[string]any{
			"source_call_id": source.CallID, "subject_key": subject, "inquiry_id": source.InquiryID,
			"drone_id": source.DroneID, "actor_kind": "drone", "operation": "saved_reassessment",
		}}
		client, err := NewMetaClient(provider, log)
		if err != nil {
			return err
		}
		log.Log("meta_queen", "meta_call_start", map[string]any{"prompt_key": "assess", "model": request.Model, "messages": request.Messages, "outcome": "started"})
		response, callErr := client.Complete(ctx, TextRequest{
			Scope: "meta_queen", Model: request.Model, Messages: request.Messages, ReasoningEffort: request.ReasoningEffort,
			PromptCacheKey: request.PromptCacheKey, SafetyIdentifier: request.SafetyIdentifier,
		})
		if callErr == nil && response.Refusal != "" {
			callErr = fmt.Errorf("assessment refused: %s", response.Refusal)
		}
		done := map[string]any{"prompt_key": "assess", "model": response.Model, "outcome": "answered", "reply": response.Text, "refusal": response.Refusal, "usage": response.Usage, "raw": string(response.Raw)}
		if callErr != nil {
			done["outcome"], done["error"] = "error", callErr.Error()
		}
		log.Log("meta_queen", "meta_call_done", done)
		var assessments []CriterionAssessment
		if callErr == nil {
			assessments, callErr = ParseAssessment(response.Text, input.ScoringElements)
			fields := map[string]any{"outcome": "parsed", "reply": response.Text, "criterion_count": len(assessments)}
			if callErr != nil {
				fields["outcome"], fields["error"] = "error", callErr.Error()
			}
			log.Log("meta_queen", "assessment_parse", fields)
		}
		result := map[string]any{"source_call_id": source.CallID, "subject": subject, "drone_id": source.DroneID,
			"status": "completed", "model": response.Model, "reply": response.Text, "assessments": assessments,
			"usage": response.Usage, "refusal": response.Refusal, "raw_response": string(response.Raw)}
		if callErr != nil {
			result["status"], result["error"] = "failed", callErr.Error()
		}
		if saveErr := writeReassessmentArtifact(dir, prefix+"-result.json", result); saveErr != nil {
			return errors.Join(callErr, saveErr)
		}
		if callErr != nil {
			return fmt.Errorf("reassessment source call %s: %w", source.CallID, callErr)
		}
	}
	return nil
}

func writeReassessmentArtifact(dir, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeNew(filepath.Join(dir, name), data)
}
