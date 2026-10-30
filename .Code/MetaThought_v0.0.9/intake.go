package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DraftQuestionContract sends only the user's intake and the unchanged contract
// prompt. It returns the original reply even on refusal; the caller saves it
// before parsing. Draft settings never supply objectives or scoring elements.
func DraftQuestionContract(ctx context.Context, c Config, client TextClient, logger EventSink, question, priorDraft, feedback string) (string, error) {
	if strings.TrimSpace(question) == "" {
		return "", errors.New("a user question is required")
	}
	if strings.TrimSpace(c.Intake.Instructions) == "" || strings.TrimSpace(c.Prompts["contract"]) == "" {
		return "", errors.New("intake instructions and the contract prompt are required")
	}
	if strings.TrimSpace(c.Models.Queen) == "" {
		return "", errors.New("the Queen model is required")
	}
	log := func(event string, fields map[string]any) {
		if logger != nil {
			logger.Log("queen", event, fields)
		}
	}
	if client == nil {
		var err error
		client, err = NewMetaClient(c.Meta, logger)
		if err != nil {
			return "", err
		}
	} else if concrete, ok := client.(*MetaClient); ok {
		copy := *concrete
		copy.Config, copy.Log = c.Meta, logger
		client = &copy
	}
	input := map[string]string{"instructions": c.Intake.Instructions, "question": question}
	if priorDraft != "" {
		input["prior_draft"] = priorDraft
	}
	if feedback != "" {
		input["feedback"] = feedback
	}
	request := TextRequest{Scope: "queen", Model: c.Models.Queen, Messages: []Message{
		{"role": "developer", "content": c.Prompts["contract"]},
		{"role": "user", "content": jsonText(input)},
	}}
	log("contract_draft_start", map[string]any{"model": request.Model, "actor_kind": "queen", "input": input, "prompt": c.Prompts["contract"]})
	reply, err := client.Complete(ctx, request)
	text := reply.Text
	if text == "" && reply.Refusal != "" {
		text = reply.Refusal
	}
	fields := map[string]any{"model": reply.Model, "response": reply.Text, "refusal": reply.Refusal, "usage": reply.Usage, "raw": string(reply.Raw), "outcome": "received"}
	if reply.Refusal != "" {
		err = errors.Join(err, &ProviderError{Kind: "refusal", Message: "contract draft refused"})
	}
	if err == nil && strings.TrimSpace(reply.Text) == "" {
		err = errors.New("contract draft returned no text")
	}
	if err != nil {
		fields["outcome"], fields["error"] = "error", err.Error()
	}
	log("contract_draft_response", fields)
	return text, err
}

// PlanQuestion is the shared CLI boundary for base and meta proposals. Only the
// diagnostic database is opened. An exclusive output is reserved before the
// call, and every returned byte is saved before refusal or format validation.
func PlanQuestion(ctx context.Context, c Config, dir, output, question, priorDraft, feedback string, client TextClient, metaPlan bool) (string, error) {
	if output == "" {
		return "", errors.New("a new draft output path is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	logger, err := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		return "", err
	}
	defer logger.Close()
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("preserve draft output: %w", err)
	}
	defer file.Close()
	var text string
	var draftErr error
	scope := "queen"
	if metaPlan {
		scope = "meta_queen"
		text, draftErr = DraftMetaContract(ctx, c, client, logger, question, priorDraft, feedback)
	} else {
		text, draftErr = DraftQuestionContract(ctx, c, client, logger, question, priorDraft, feedback)
	}
	_, writeErr := file.WriteString(text)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return text, errors.Join(draftErr, writeErr, closeErr)
	}
	logger.Log(scope, "contract_draft_saved", map[string]any{"path": output, "outcome": "saved"})
	if draftErr == nil {
		if _, err = ParseContract(text); err != nil {
			draftErr = fmt.Errorf("contract draft: %w", err)
		}
	}
	if draftErr != nil {
		logger.Log(scope, "contract_rejected", map[string]any{"path": output, "outcome": "error", "error": draftErr.Error()})
		return text, draftErr
	}
	logger.Log(scope, "contract_drafted", map[string]any{"path": output, "outcome": "awaiting_user_confirmation"})
	return text, nil
}
