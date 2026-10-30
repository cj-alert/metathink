package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// This only projects internal prompt context. It never edits stored evidence or replies.
type PromptContextConfig struct {
	Views           map[string][]string `json:"views"`
	OmitFields      []string            `json:"omit_fields"`
	KeepURLFields   []string            `json:"keep_url_fields"`
	RecordFields    []string            `json:"record_fields"`
	ResearchFields  []string            `json:"research_fields"`
	StripSourceURLs bool                `json:"strip_source_urls"`
}

var promptURL = regexp.MustCompile(`https?://[^\s<>"\]]+`)
var promptLink = regexp.MustCompile(`\[([^\]]*)\]\(https?://[^\s]*?\)`)

func promptContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func promptDecode(text string) (any, bool) {
	if !json.Valid([]byte(text)) {
		return nil, false
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	return value, true
}

func promptClean(text string, cfg *PromptContextConfig, keepURLs bool) string {
	if keepURLs || !cfg.StripSourceURLs {
		return text
	}
	text = promptLink.ReplaceAllString(text, "$1")
	return promptURL.ReplaceAllStringFunc(text, func(raw string) string {
		// Keep trailing prose punctuation when removing a bare source address.
		trimmed := strings.TrimRight(raw, ".,;:)")
		return strings.TrimPrefix(raw, trimmed)
	})
}

func promptWrite(b *strings.Builder, value any, cfg *PromptContextConfig, keepURLs bool) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		if decoded, ok := promptDecode(v); ok {
			switch decoded.(type) {
			case map[string]any, []any:
				promptWrite(b, decoded, cfg, keepURLs)
				return
			}
		}
		b.WriteString(promptClean(v, cfg, keepURLs))
		b.WriteByte('\n')
	case []any:
		for _, item := range v {
			promptWrite(b, item, cfg, keepURLs)
		}
	case map[string]any:
		if body, ok := v["body"].(string); ok && v["id"] != nil && v["kind"] != nil {
			for _, key := range cfg.RecordFields {
				if key == "body" {
					continue
				}
				if item, found := v[key]; found && item != nil {
					fmt.Fprintf(b, "%s: %v\n", key, item)
				}
			}
			if record, ok := promptDecode(body); ok {
				if research, ok := record.(map[string]any); ok && research["status"] != nil && research["question"] != nil {
					if date, ok := v["created"].(string); ok {
						fmt.Fprintf(b, "recorded: %s\n", date)
					}
					for _, key := range cfg.ResearchFields {
						if item, found := research[key]; found && item != nil {
							b.WriteString(key + ":\n")
							promptWrite(b, item, cfg, keepURLs || promptContains(cfg.KeepURLFields, key))
						}
					}
					return
				}
			}
			b.WriteString("body:\n")
			promptWrite(b, body, cfg, keepURLs)
		} else {
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if promptContains(cfg.OmitFields, key) || v[key] == nil {
					continue
				}
				b.WriteString(key + ":\n")
				if text, ok := v[key].(string); ok && promptContains(cfg.KeepURLFields, key) {
					b.WriteString(text)
					b.WriteByte('\n')
				} else {
					promptWrite(b, v[key], cfg, keepURLs)
				}
			}
		}
	default:
		fmt.Fprintln(b, v)
	}
}

func promptContextText(text, kind string, cfg *PromptContextConfig, web bool) string {
	if cfg == nil || kind == "" {
		return text
	}
	value, ok := promptDecode(text)
	if !ok {
		return text
	}
	if root, ok := value.(map[string]any); ok {
		if fields, selected := cfg.Views[kind]; selected {
			view := map[string]any{}
			for _, key := range fields {
				if item, found := root[key]; found {
					view[key] = item
				}
			}
			root = view
		}
		// Explicit history already contains every research record; don't repeat it in work/latest.
		if root["research_history"] != nil {
			delete(root, "research_work")
			if rows, ok := root["work"].([]any); ok {
				work := make([]any, 0, len(rows))
				for _, row := range rows {
					m, ok := row.(map[string]any)
					if !ok || m["kind"] != "RESEARCH" {
						work = append(work, row)
					}
				}
				root["work"] = work
			}
		}
		value = root
	}
	var b strings.Builder
	promptWrite(&b, value, cfg, web)
	return b.String()
}

func promptContextMessages(in TextRequest, cfg *PromptContextConfig) []Message {
	if cfg == nil || in.ContextKind == "" {
		return in.Messages
	}
	messages := make([]Message, len(in.Messages))
	for i, original := range in.Messages {
		m := Message{}
		for key, value := range original {
			m[key] = value
		}
		messages[i] = m
		if m["role"] != "user" {
			continue
		}
		switch content := m["content"].(type) {
		case string:
			m["content"] = promptContextText(content, in.ContextKind, cfg, in.WebSearch)
		case []any:
			parts := make([]any, len(content))
			for j, item := range content {
				if part, ok := item.(map[string]any); ok && part["type"] == "text" {
					copy := map[string]any{}
					for key, value := range part {
						copy[key] = value
					}
					if text, ok := part["text"].(string); ok {
						copy["text"] = promptContextText(text, in.ContextKind, cfg, in.WebSearch)
					}
					parts[j] = copy
				} else {
					parts[j] = item
				}
			}
			m["content"] = parts
		}
	}
	return messages
}

func executePromptContext(args []string) error {
	f := flag.NewFlagSet("prompt-context", flag.ContinueOnError)
	config := f.String("config", "config.json", "configuration")
	kind := f.String("kind", "", "internal call kind")
	input := f.String("input", "", "saved structured context file")
	out := f.String("out", "", "new plain-text output file")
	web := f.Bool("web", false, "preserve URLs for actual research context")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *kind == "" || *input == "" || *out == "" {
		return errors.New("prompt-context requires -kind, -input and -out")
	}
	c, err := LoadConfig(*config)
	if err != nil {
		return err
	}
	source, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	result := promptContextText(string(source), *kind, c.Meta.PromptContext, *web)
	if err = writeNew(*out, []byte(result)); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"out": *out, "input_chars": len([]rune(string(source))), "output_chars": len([]rune(result)), "model_calls": 0})
}
