package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

const Version = "0.0.11"

type WorkerSettings struct {
	Count      int    `json:"count"`
	Observe    string `json:"observe"`
	IdlePollMS int    `json:"idle_poll_ms"`
}

type DashboardSettings struct {
	Enabled          bool   `json:"enabled"`
	RefreshSeconds   int    `json:"refresh_seconds"`
	File             string `json:"file"`
	InstanceName     string `json:"instance_name,omitempty"`
	PublishDirectory string `json:"publish_directory,omitempty"`
}

type HTTP500SMSSettings struct {
	Enabled    bool   `json:"enabled"`
	HelperPath string `json:"helper_path"`
	Project    string `json:"project"`
	ChatLabel  string `json:"chat_label"`
}

type AlertSettings struct {
	HTTP500SMS HTTP500SMSSettings `json:"http_500_sms"`
}

type Config struct {
	Alerts    AlertSettings     `json:"alerts,omitempty"`
	Workers   WorkerSettings    `json:"workers"`
	Dashboard DashboardSettings `json:"dashboard"`
	Intake    struct {
		Instructions string `json:"instructions"`
	} `json:"intake"`
	Version   string          `json:"version"`
	Meta      MetaConfig      `json:"meta"`
	MetaQueen MetaQueenConfig `json:"meta_queen"`
	Models    struct {
		Drone string `json:"drone"`
		Queen string `json:"queen"`
	} `json:"models"`
	Files struct {
		State    string `json:"state"`
		Debug    string `json:"debug"`
		Contract string `json:"contract"`
		Result   string `json:"result"`
	} `json:"files"`
	Hive struct {
		DroneCount int   `json:"drone_count"`
		Seed       int64 `json:"seed"`
	} `json:"hive"`
	Experiment struct {
		Objective string      `json:"objective"`
		Intent    string      `json:"intent"`
		Context   string      `json:"context"`
		Criteria  []Criterion `json:"criteria"`
	} `json:"experiment"`
	Chromosomes []Chromosome      `json:"chromosomes"`
	Decisions   DecisionPrompts   `json:"decisions"`
	Prompts     map[string]string `json:"prompts"`
	Schema      struct {
		State []string `json:"state"`
		Logs  []string `json:"logs"`
	} `json:"schema"`
}

type Criterion struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Levels      []DecisionLevel `json:"levels"`
	MinScore    *float64        `json:"min_score,omitempty"`
	Weight      float64         `json:"weight"`
}

type Chromosome struct {
	Contribute string `json:"contribute"`
	Expand     string `json:"expand"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.Version != Version {
		return fmt.Errorf("config version %q does not match %s", c.Version, Version)
	}
	if c.Models.Drone == "" || c.Models.Queen == "" {
		return fmt.Errorf("both model IDs are required")
	}
	if err := c.Meta.HTTP500Retry.validate(); err != nil {
		return err
	}
	if s := c.Alerts.HTTP500SMS; s.Enabled && (strings.TrimSpace(s.HelperPath) == "" || strings.TrimSpace(s.Project) == "" || strings.TrimSpace(s.ChatLabel) == "") {
		return fmt.Errorf("enabled HTTP500 SMS requires helper_path, project, and chat_label")
	}
	seen := map[string]bool{}
	for _, name := range []string{c.Files.State, c.Files.Debug, c.Files.Contract, c.Files.Result} {
		if name == "" || name == "." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\") || seen[strings.ToLower(name)] {
			return fmt.Errorf("runtime filenames must be distinct flat filenames")
		}
		seen[strings.ToLower(name)] = true
	}
	if c.Dashboard.Enabled {
		if err := c.Dashboard.validate(); err != nil {
			return err
		}
		name := strings.ReplaceAll(c.Dashboard.File, "{instance}", c.Dashboard.InstanceName)
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("dashboard filename must differ from operational runtime files")
		}
	}
	if c.Hive.DroneCount < 2 {
		return fmt.Errorf("at least two initial drones are required to form a team")
	}

	if len(c.Chromosomes) == 0 {
		return fmt.Errorf("chromosomes are required")
	}
	for _, pair := range c.Chromosomes {
		if strings.TrimSpace(pair.Contribute) == "" || strings.TrimSpace(pair.Expand) == "" {
			return fmt.Errorf("each chromosome pair needs both instructions")
		}
	}
	for _, key := range []string{"contract", "fit", "invite", "commit", "contribute", "expand", "recipient", "candidate", "assess", "collapse", "collapse_format", "guide"} {
		if strings.TrimSpace(c.Prompts[key]) == "" {
			return fmt.Errorf("missing prompt %s", key)
		}
		if _, err := template.New(key).Option("missingkey=error").Parse(c.Prompts[key]); err != nil {
			return err
		}
	}
	if len(c.Schema.State) == 0 || len(c.Schema.Logs) == 0 {
		return fmt.Errorf("state and log schemas are required")
	}
	return nil
}

func renderPrompt(source string, data any) (string, error) {
	t, err := template.New("prompt").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	err = t.Execute(&out, data)
	return out.String(), err
}

func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }

// Draft settings seed planning only; confirmed requests never read their criteria.
func (c Config) ValidateDraft() error {
	if strings.TrimSpace(c.Experiment.Objective) == "" || len(c.Experiment.Criteria) == 0 {
		return fmt.Errorf("draft objective and criteria are required")
	}
	seen := map[string]bool{}
	for _, v := range c.Experiment.Criteria {
		if v.Name == "" || seen[v.Name] || v.Description == "" || len(v.Levels) < 2 || v.Weight < 0 {
			return fmt.Errorf("invalid criterion %q", v.Name)
		}
		seen[v.Name] = true
		if v.MinScore != nil && !contractUnitValue(*v.MinScore) {
			return fmt.Errorf("draft criterion %s target must be finite and in [0,1]", v.Name)
		}
	}

	return nil
}
