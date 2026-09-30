// Package store writes JSONL attempt records per spec.md Section 4.1.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record is one attempt as defined in spec.md Section 4.1 (store module).
type Record struct {
	Ts            string         `json:"ts"`
	TaskID        string         `json:"task_id"`
	RunID         string         `json:"run_id,omitempty"`
	Model         string         `json:"model"`
	Attempt       int            `json:"attempt"`
	FilesTouched  []string       `json:"files_touched,omitempty"`
	DiffBytes     int            `json:"diff_bytes,omitempty"`
	Applied       bool           `json:"applied"`
	BuildOK       bool           `json:"build_ok,omitempty"`
	TestsOK       bool           `json:"tests_ok,omitempty"`
	HumanVerdict  string         `json:"human_verdict,omitempty"`
	VerdictReason string         `json:"verdict_reason,omitempty"`
	ErrorClass    string         `json:"error_class,omitempty"`
	Supervisor    map[string]any `json:"supervisor,omitempty"`
	DurationMS    int64          `json:"duration_ms,omitempty"`
}

// Writer appends records to a JSONL file. Safe for concurrent use —
// cmd/batch (Phase 4) may run tasks in parallel later.
type Writer struct {
	mu   sync.Mutex
	path string
}

// NewWriter returns a writer rooted at dir (defaults to results/) with
// filename results.jsonl, creating the directory if needed.
func NewWriter(dir string) (*Writer, error) {
	if dir == "" {
		dir = "results"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: mkdir %s: %w", dir, err)
	}
	return &Writer{path: filepath.Join(dir, "results.jsonl")}, nil
}

// Append marshals the record (filling Ts if empty) and appends it as one
// JSONL line. Marshal failures are programming errors and panic; I/O
// errors are returned.
func (w *Writer) Append(r Record) error {
	if r.Ts == "" {
		r.Ts = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(r)
	if err != nil {
		panic(fmt.Sprintf("store: marshal record (programming error): %v", err))
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("store: open %s: %w", w.path, err)
	}
	defer f.Close()

	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("store: write %s: %w", w.path, err)
	}
	return nil
}

// WriteRecord is the convenience one-shot used by cmd/agent: construct a
// default writer and append a single record.
func WriteRecord(r Record) error {
	w, err := NewWriter("results")
	if err != nil {
		return err
	}
	return w.Append(r)
}
