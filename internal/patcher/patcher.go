// Package patcher assembles the agent prompt, parses model output into
// full-file contents, and materializes them as a git diff in a temp
// workspace.
//
// Output contract v2 (see technicaldebt.md): the model writes complete
// file contents rather than unified diffs. Asking a model to hand-count
// hunk line numbers is incidental complexity; git computes the diff.
package patcher

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/amangale/patch-rig/internal/contextloader"
	"github.com/amangale/patch-rig/internal/task"
)

var (
	// ErrNoFiles means the response contained no file blocks.
	ErrNoFiles = errors.New("patcher: response contained no file blocks")
	// ErrBadPath means a declared file path escaped the repo root.
	ErrBadPath = errors.New("patcher: file path escapes repository root")
)

const promptHeader = `You are a coding agent operating on a Go repository. Complete the task by writing complete files.

TASK:
%s

OUTPUT CONTRACT — follow exactly:
- Respond with one fenced code block PER FILE you create or modify.
- Immediately before each code block, write a single line: FILE: <relative path>
  Example: FILE: internal/stats/stats.go
- Every block must contain the COMPLETE new content of that file, not a fragment or diff.
- Paths are relative to the repository root. Never prefix with the repo name.
- Only output files that must change. Do not repeat unchanged files.
- No commentary outside the FILE lines and code blocks.`

// FileBlock is one parsed FILE block from the model response.
type FileBlock struct {
	Path    string
	Content string
}

var (
	fileLineRe = regexp.MustCompile(`(?m)^FILE:\s*(\S+)\s*$`)
	fenceRe    = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_-]+)?\\s*\\n(.*?)```")
)

// BuildPrompt assembles the full prompt: header contract, task, file contents.
func BuildPrompt(t task.Task, files []contextloader.File) string {
	var b strings.Builder
	fmt.Fprintf(&b, promptHeader, t.Description)
	b.WriteString("\n\nCURRENT REPOSITORY FILES (paths relative to root):\n")
	for _, f := range files {
		fmt.Fprintf(&b, "\n---- %s ----\n%s\n", f.Path, f.Content)
	}
	if t.VerificationHint != "" {
		fmt.Fprintf(&b, "\nVERIFICATION: %s\n", t.VerificationHint)
	}
	return b.String()
}

// ParseResponse extracts all FILE blocks from a model response. Blocks are
// returned sorted by path; duplicate declarations of the same path are a
// parse error (ambiguous output).
// ParseResponse extracts all FILE blocks from a model response. Blocks are
// returned sorted by path; duplicate declarations of the same path are a
// parse error (ambiguous output).
func ParseResponse(resp string) ([]FileBlock, error) {
	parts := fileLineRe.Split(resp, -1)
	declared := fileLineRe.FindAllStringSubmatch(resp, -1)
	if len(declared) == 0 {
		return nil, ErrNoFiles
	}
	fenceRe := regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_-]+)?\\s*\\n(.*?)```")

	blocksByPath := make(map[string]string, len(declared))
	for i, decl := range declared {
		rest := parts[i+1]
		fm := fenceRe.FindStringSubmatch(rest)
		if fm == nil {
			return nil, fmt.Errorf("patcher: FILE %s has no code block after it", decl[1])
		}
		if _, dup := blocksByPath[decl[1]]; dup {
			return nil, fmt.Errorf("patcher: duplicate FILE declaration for %s", decl[1])
		}
		blocksByPath[decl[1]] = fm[1]
	}

	var out []FileBlock
	for p, c := range blocksByPath {
		out = append(out, FileBlock{Path: p, Content: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ValidateBlocks rejects path traversal and non-Go junk before anything
// touches the workspace.
func ValidateBlocks(blocks []FileBlock, allowedRoots []string) error {
	for _, b := range blocks {
		clean := path.Clean("/" + b.Path)
		clean = strings.TrimPrefix(clean, "/")
		if clean != b.Path || b.Path == "" || strings.Contains(b.Path, "..") {
			return fmt.Errorf("%w: %q", ErrBadPath, b.Path)
		}
		if filepath.IsAbs(b.Path) {
			return fmt.Errorf("%w: absolute path %q", ErrBadPath, b.Path)
		}
	}
	return nil
}

// Materialize writes the file blocks into the workspace and returns the
// resulting diff. The workspace must already be a git repo with an initial
// commit (see PrepareWorkspace).
func Materialize(workdir string, blocks []FileBlock) (string, error) {
	for _, b := range blocks {
		target := filepath.Join(workdir, filepath.FromSlash(b.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", fmt.Errorf("patcher: mkdir for %s: %w", b.Path, err)
		}
		content := b.Content
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("patcher: write %s: %w", b.Path, err)
		}
	}

	cmd := exec.Command("git", "-c", "core.safecrlf=false", "diff", "--no-color")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("patcher: git diff: %w", err)
	}
	diff := string(out)
	if strings.TrimSpace(diff) == "" {
		return "", fmt.Errorf("patcher: materialized blocks produced an empty diff")
	}
	return diff, nil
}

// PrepareWorkspace copies the target repo to a fresh temp dir and makes it
// a git repo with one initial commit, so Materialize can produce a clean
// relative-path diff of exactly the agent's changes. The original tree is
// never touched.
func PrepareWorkspace(src string) (string, error) {
	dst, err := os.MkdirTemp("", "patch-rig-ws-*")
	if err != nil {
		return "", fmt.Errorf("patcher: temp workspace: %w", err)
	}
	skip := map[string]struct{}{
		".git": {}, "vendor": {}, "testdata": {}, "node_modules": {}, "results": {},
	}
	err = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src {
			return nil
		}
		if d.IsDir() {
			if _, s := skip[d.Name()]; s {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, p[len(src):]), 0o755)
		}
		if strings.HasPrefix(d.Name(), ".env") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, p[len(src):]), data, 0o644)
	})
	if err != nil {
		os.RemoveAll(dst)
		return "", fmt.Errorf("patcher: copy %s: %w", src, err)
	}

	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dst
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=patch-rig", "GIT_AUTHOR_EMAIL=patch-rig@local",
			"GIT_COMMITTER_NAME=patch-rig", "GIT_COMMITTER_EMAIL=patch-rig@local")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"commit", "-q", "-m", "baseline"},
	} {
		if err := git(args...); err != nil {
			os.RemoveAll(dst)
			return "", fmt.Errorf("patcher: %w", err)
		}
	}
	return dst, nil
}
