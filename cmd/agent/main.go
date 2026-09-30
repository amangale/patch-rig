// Command agent runs a single patch-rig task: load task -> context load ->
// propose -> parse diff -> apply check in a temp workspace. Phase 2 adds
// automated verification and result recording.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/amangale/patch-rig/internal/contextloader"
	"github.com/amangale/patch-rig/internal/llm"
	"github.com/amangale/patch-rig/internal/patcher"
	"github.com/amangale/patch-rig/internal/store"
	"github.com/amangale/patch-rig/internal/task"
)

func main() {
	var (
		taskPath = flag.String("task", "", "path to task JSONL file (required)")
		repoFlag = flag.String("repo", "", "target repo root (overrides task target_repo)")
		model    = flag.String("model", "qwen2.5:7b", "pinned Ollama model")
		ollama   = flag.String("ollama", "http://localhost:11434", "Ollama base URL")
		dryRun   = flag.Bool("dry-run", false, "print assembled prompt and exit (no model call)")
	)
	flag.Parse()

	if *taskPath == "" {
		log.Fatal("agent: -task is required")
	}

	start := time.Now()
	t, err := task.Load(*taskPath)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}

	repo := *repoFlag
	if repo == "" {
		repo = t.TargetRepo
	}
	if repo == "" {
		log.Fatal("agent: no target repo: set -repo or target_repo in the task")
	}

	loader := contextloader.New()
	files, err := loader.Load(repo, t.ExpectedFiles)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	fmt.Printf("task=%s repo=%s context_files=%d context_bytes=%d\n",
		t.TaskID, repo, len(files), totalBytes(files))

	prompt := patcher.BuildPrompt(*t, files)
	if *dryRun {
		fmt.Println("--- PROMPT ---")
		fmt.Print(prompt)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := llm.New(*ollama, *model)
	fmt.Printf("generating with %s ...\n", *model)
	genStart := time.Now()
	resp, err := client.Generate(ctx, prompt)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	fmt.Printf("generated in %s (%d chars)\n", time.Since(genStart).Round(time.Millisecond), len(resp))

	blocks, err := patcher.ParseResponse(resp)
	if err != nil {
		writeArtifact(t.TaskID, 1, resp)
		recordFailure(t.TaskID, *model, "PARSE_ERROR", len(resp), time.Since(start))
		log.Fatalf("agent: %v (raw response saved to results/)", err)
	}
	if err := patcher.ValidateBlocks(blocks, nil); err != nil {
		writeArtifact(t.TaskID, 1, resp)
		recordFailure(t.TaskID, *model, "BAD_PATH", len(resp), time.Since(start))
		log.Fatalf("agent: %v (raw response saved to results/)", err)
	}
	for _, b := range blocks {
		fmt.Printf("  file: %s (%d bytes)\n", b.Path, len(b.Content))
	}

	ws, err := patcher.PrepareWorkspace(repo)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	defer os.RemoveAll(ws)

	diff, err := patcher.Materialize(ws, blocks)
	if err != nil {
		writeArtifact(t.TaskID, 1, resp)
		recordFailure(t.TaskID, *model, "EMPTY_OR_FAILED_DIFF", len(resp), time.Since(start))
		log.Fatalf("agent: %v", err)
	}

	writeArtifact(t.TaskID, 1, diff)
	recordSuccessDiff(t.TaskID, *model, blocks, diff, time.Since(start))
	fmt.Printf("OK: diff materialized from %d file blocks (task=%s, %s)\n",
		len(blocks), t.TaskID, time.Since(start).Round(time.Millisecond))
}

func totalBytes(files []contextloader.File) int {
	var n int
	for _, f := range files {
		n += f.Size
	}
	return n
}

func writeArtifact(taskID string, attempt int, content string) {
	if err := os.MkdirAll("results", 0o755); err != nil {
		log.Printf("agent: artifact dir: %v", err)
		return
	}
	name := filepath.Join("results", fmt.Sprintf("%s-a%d.txt", taskID, attempt))
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		log.Printf("agent: write artifact: %v", err)
	}
}

func recordFailure(taskID, model, class string, respLen int, d time.Duration) {
	if err := store.WriteRecord(store.Record{
		TaskID:     taskID,
		Model:      model,
		Attempt:    1,
		Applied:    false,
		ErrorClass: class,
		DurationMS: d.Milliseconds(),
	}); err != nil {
		log.Printf("agent: record failure: %v", err)
	}
}

func recordSuccessDiff(taskID, model string, blocks []patcher.FileBlock, diff string, d time.Duration) {
	var files []string
	for _, b := range blocks {
		files = append(files, b.Path)
	}
	if err := store.WriteRecord(store.Record{
		TaskID:       taskID,
		Model:        model,
		Attempt:      1,
		Applied:      true,
		FilesTouched: files,
		DiffBytes:    len(diff),
		DurationMS:   d.Milliseconds(),
	}); err != nil {
		log.Printf("agent: record success: %v", err)
	}
}
