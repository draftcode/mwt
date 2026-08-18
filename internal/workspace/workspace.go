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
	"github.com/draftcode/mwt/internal/gh"
	"github.com/draftcode/mwt/internal/ghstack"
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

// Match is one worktree located by a search across workspaces. Branch is the
// branch the search matched, which is not always the one CheckedOut: a stack
// keeps several branches in a single worktree.
type Match struct {
	Workspace  string
	Repo       string
	Path       string
	Branch     string
	CheckedOut string
}

// FindByBranch returns every worktree holding branch, restricted to a single repo
// name when repo is non-empty. A worktree holds a branch when it has it checked
// out or when gh stack tracks it there, so a branch further down a stack still
// resolves to the worktree it was created in.
func FindByBranch(cfg *config.Config, branch, repo string) ([]Match, error) {
	return search(cfg, repo, func(current string, stack []ghstack.Branch) (string, bool) {
		if current == branch {
			return branch, true
		}
		for _, b := range stack {
			if b.Name == branch {
				return branch, true
			}
		}
		return "", false
	})
}

// FindByPR returns every worktree whose gh stack state records the pull request,
// whatever branch the worktree currently has checked out.
func FindByPR(cfg *config.Config, ref gh.Ref) ([]Match, error) {
	return search(cfg, "", func(current string, stack []ghstack.Branch) (string, bool) {
		for _, b := range stack {
			if b.PRURL != "" && ref.Matches(b.PRURL) {
				return b.Name, true
			}
		}
		return "", false
	})
}

// search walks every worktree of every workspace and collects the ones keep
// accepts, given the checked-out branch and the stack gh stack tracks there. keep
// returns the branch the match is about, which need not be the checked-out one.
func search(cfg *config.Config, repo string, keep func(current string, stack []ghstack.Branch) (string, bool)) ([]Match, error) {
	all, err := List(cfg)
	if err != nil {
		return nil, err
	}
	var out []Match
	for _, ws := range all {
		for _, r := range ws.Repos {
			if repo != "" && r.Name != repo {
				continue
			}
			// A worktree can sit on a branch of its own, so the checkout wins over
			// the workspace's recorded branch.
			current := ws.Branch
			if s, err := git.Describe(r.Path); err == nil && s.Branch != "" {
				current = s.Branch
			}
			// An unreadable stack is one worktree's problem, not the search's.
			stack, _ := ghstack.Branches(r.Path)
			branch, ok := keep(current, stack)
			if !ok {
				continue
			}
			out = append(out, Match{Workspace: ws.Name, Repo: r.Name, Path: r.Path, Branch: branch, CheckedOut: current})
		}
	}
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
