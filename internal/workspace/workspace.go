// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

// Package workspace models a named set of git worktrees spanning several repos.
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/draftcode/mwt/internal/config"
	"github.com/draftcode/mwt/internal/git"
)

// MetaFile is the per-workspace state file stored at the workspace root.
const MetaFile = ".mwt.json"

// Repo is one repo checked out into a workspace.
type Repo struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
}

// Workspace is a named directory holding one worktree per participating repo.
type Workspace struct {
	Name    string    `json:"name"`
	Branch  string    `json:"branch"`
	Root    string    `json:"-"`
	Created time.Time `json:"created"`
	Repos   []Repo    `json:"repos"`
}

// DirName converts a workspace name into a single directory component.
func DirName(name string) string {
	return strings.ReplaceAll(name, "/", "-")
}

// Load reads the workspace metadata stored at root.
func Load(root string) (*Workspace, error) {
	data, err := os.ReadFile(filepath.Join(root, MetaFile))
	if err != nil {
		return nil, err
	}
	ws := &Workspace{}
	if err := json.Unmarshal(data, ws); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Join(root, MetaFile), err)
	}
	ws.Root = root
	ws.adoptUnrecorded()
	return ws, nil
}

// adoptUnrecorded adds worktrees sitting in the workspace directory that the
// metadata does not mention.
//
// An interrupted or failed hydration leaves a worktree on disk unrecorded, and an
// unseen worktree is an unprotected one: removal skips its uncommitted work and
// deletes the directory regardless. Adopting in memory keeps every command honest
// without rewriting state behind the user's back. Recorded entries whose directory
// is gone are left alone, so they are still reported rather than quietly dropped.
func (w *Workspace) adoptUnrecorded() {
	entries, err := os.ReadDir(w.Root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := w.Repo(e.Name()); ok {
			continue
		}
		path := filepath.Join(w.Root, e.Name())
		if !git.IsRepo(path) {
			continue
		}
		source, err := git.MainWorktree(path)
		if err != nil {
			continue
		}
		w.Repos = append(w.Repos, Repo{Name: e.Name(), Source: source, Path: path})
	}
}

// Save writes the workspace metadata back to disk.
func (w *Workspace) Save() error {
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.Root, MetaFile), append(data, '\n'), 0o644)
}

// Repo returns the named repo entry, if it is part of the workspace.
func (w *Workspace) Repo(name string) (Repo, bool) {
	for _, r := range w.Repos {
		if r.Name == name {
			return r, true
		}
	}
	return Repo{}, false
}

// List returns every workspace under the configured worktree root.
func List(cfg *config.Config) ([]*Workspace, error) {
	entries, err := os.ReadDir(cfg.WorktreeRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Workspace
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ws, err := Load(filepath.Join(cfg.WorktreeRoot, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, ws)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Find resolves a workspace by name, or by the current directory when name is empty.
func Find(cfg *config.Config, name string) (*Workspace, error) {
	if name == "" {
		return Current(cfg)
	}
	root := filepath.Join(cfg.WorktreeRoot, DirName(name))
	ws, err := Load(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no workspace %q under %s", name, cfg.WorktreeRoot)
	}
	return ws, err
}

// Current walks up from the working directory to find the enclosing workspace.
func Current(cfg *config.Config) (*Workspace, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, MetaFile)); err == nil {
			return Load(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, fmt.Errorf("not inside a workspace; pass a workspace name")
		}
		dir = parent
	}
}

// ResolveSource locates a repo's canonical checkout by name or path.
func ResolveSource(cfg *config.Config, nameOrPath string) (name, path string, err error) {
	if r, ok := cfg.Repos[nameOrPath]; ok && r.Path != "" {
		p := config.Expand(r.Path)
		if git.IsRepo(p) {
			return nameOrPath, p, nil
		}
		return "", "", fmt.Errorf("configured path for %q is not a git repo: %s", nameOrPath, p)
	}
	if strings.Contains(nameOrPath, "/") || strings.HasPrefix(nameOrPath, ".") {
		p, err := filepath.Abs(config.Expand(nameOrPath))
		if err != nil {
			return "", "", err
		}
		if !git.IsRepo(p) {
			return "", "", fmt.Errorf("%s is not a git repo", p)
		}
		top, err := git.Run(p, "rev-parse", "--show-toplevel")
		if err != nil {
			return "", "", err
		}
		return filepath.Base(top), top, nil
	}
	for _, base := range cfg.RepoSearchPaths {
		p := filepath.Join(base, nameOrPath)
		if git.IsRepo(p) {
			return nameOrPath, p, nil
		}
	}
	return "", "", fmt.Errorf("repo %q not found under %s", nameOrPath, strings.Join(cfg.RepoSearchPaths, ", "))
}
