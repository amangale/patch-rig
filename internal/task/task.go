// Package task defines the task contract for patch-rig and loads task
// definitions from JSONL files.
package task

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Task is one unit of work for the agent, per spec.md Section 3.
type Task struct {
	TaskID           string   `json:"task_id"`
	Description      string   `json:"description"`
	TargetRepo       string   `json:"target_repo,omitempty"` // optional; -repo flag overrides
	ExpectedFiles    []string `json:"expected_files"`
	VerificationHint string   `json:"verification_hint,omitempty"`
}

// Load reads the first non-empty JSONL record from path. The batch runner
// (Phase 4) will iterate all lines; the single-task agent needs exactly one.
func Load(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("task: read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var t Task
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			return nil, fmt.Errorf("task: parse %s: %w", path, err)
		}
		if t.TaskID == "" || t.Description == "" {
			return nil, fmt.Errorf("task: %s record missing task_id or description", path)
		}
		return &t, nil
	}
	return nil, fmt.Errorf("task: %s contains no records", path)
}
