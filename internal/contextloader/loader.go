// Package contextloader selects repository files for the agent prompt
// under a byte budget, per spec.md Section 4.1.
package contextloader

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File is one selected repository file.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Size    int    `json:"size"`
}

// Loader walks a target repo and selects files under a budget.
type Loader struct {
	BudgetBytes int
	SkipDirs    map[string]struct{}
}

// New returns a loader with the standing config: ~8k tokens estimated as
// bytes/4 => 32 KiB budget, skipping .git, vendor, testdata, node_modules.
func New() *Loader {
	return &Loader{
		BudgetBytes: 32 * 1024,
		SkipDirs: map[string]struct{}{
			".git":         {},
			"vendor":       {},
			"testdata":     {},
			"node_modules": {},
		},
	}
}

type candidate struct {
	path string
	size int
	prio int
}

// Load walks root and returns selected files. Priority: files listed in
// expected (in given order), then *.go, then go.mod/go.sum, then anything
// else. Oversized individual candidates are skipped, not fatal, so a small
// expected file can still be admitted after a big one busts the budget.
func (l *Loader) Load(root string, expected []string) ([]File, error) {
	expect := make(map[string]int, len(expected))
	for i, e := range expected {
		expect[e] = 1000 + i
	}

	var cands []candidate
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root {
				if _, skip := l.SkipDirs[d.Name()]; skip {
					return fs.SkipDir
				}
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".env") || name == ".gitignore" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		prio := 1
		switch {
		case strings.HasSuffix(name, ".go"):
			prio = 20
		case name == "go.mod" || name == "go.sum":
			prio = 10
		}
		if p, ok := expect[rel]; ok {
			prio = p
		}
		cands = append(cands, candidate{path: rel, size: int(info.Size()), prio: prio})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("contextloader: walk %s: %w", root, err)
	}

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].prio != cands[j].prio {
			return cands[i].prio > cands[j].prio
		}
		return cands[i].path < cands[j].path
	})

	var files []File
	used := 0
	for _, c := range cands {
		if used+c.size > l.BudgetBytes {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, c.path))
		if err != nil {
			return nil, fmt.Errorf("contextloader: read %s: %w", c.path, err)
		}
		files = append(files, File{Path: c.path, Content: string(b), Size: len(b)})
		used += c.size
	}

	// Deterministic prompt ordering: by path, not selection priority.
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}
