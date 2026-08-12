// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package workspace

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/draftcode/mwt/internal/config"
	"github.com/draftcode/mwt/internal/git"
)

// srcRepo builds a repo with one commit that worktrees can be cut from.
func srcRepo(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, "src", name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@test.invalid"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "-m", "base"},
	} {
		if _, err := git.Run(path, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return path
}

func testConfig(root string, repos map[string]config.Rep) *config.Config {
	return &config.Config{
		WorktreeRoot:    filepath.Join(root, "worktrees"),
		RepoSearchPaths: []string{filepath.Join(root, "src")},
		DefaultBase:     "main",
		Repos:           repos,
	}
}

func newWorkspace(t *testing.T, cfg *config.Config, name string) *Workspace {
	t.Helper()
	wsRoot := filepath.Join(cfg.WorktreeRoot, DirName(name))
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Name: name, Branch: name, Root: wsRoot, Created: time.Now()}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}
	return ws
}

// A setup command that fails must not cost the workspace its bookkeeping: the
// worktree exists by then, and an unrecorded worktree is an unprotected one.
func TestAddReposRecordsWorktreeWhenSetupFails(t *testing.T) {
	root := t.TempDir()
	srcRepo(t, root, "widget")
	cfg := testConfig(root, map[string]config.Rep{
		"widget": {RepoConfig: config.RepoConfig{Setup: "exit 3"}},
	})
	ws := newWorkspace(t, cfg, "feat/thing")

	err := AddRepos(cfg, ws, []string{"widget"}, AddOptions{Out: io.Discard})
	if err == nil {
		t.Fatal("AddRepos succeeded despite a failing setup command")
	}

	reloaded, err := Load(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Repo("widget"); !ok {
		t.Errorf("widget missing from %s after failed setup: %+v", MetaFile, reloaded.Repos)
	}
}

// A worktree left behind by an interrupted run is invisible to every command
// until it is adopted, and invisible means unprotected.
func TestLoadAdoptsUnrecordedWorktree(t *testing.T) {
	root := t.TempDir()
	source := srcRepo(t, root, "widget")
	cfg := testConfig(root, nil)
	ws := newWorkspace(t, cfg, "feat/thing")

	path := filepath.Join(ws.Root, "widget")
	if err := git.AddWorktree(source, path, "feat/thing", "main"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reloaded.Repo("widget")
	if !ok {
		t.Fatalf("unrecorded worktree not adopted: %+v", reloaded.Repos)
	}
	if r.Path != path {
		t.Errorf("path = %q, want %q", r.Path, path)
	}
	if r.Source != source {
		t.Errorf("source = %q, want %q", r.Source, source)
	}
}

// A recorded repo whose directory is gone must stay in the list, so commands can
// report it instead of pretending the workspace is empty.
func TestLoadKeepsRecordedRepoWithMissingDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root, nil)
	ws := newWorkspace(t, cfg, "feat/thing")
	ws.Repos = []Repo{{
		Name:   "widget",
		Source: filepath.Join(root, "src", "widget"),
		Path:   filepath.Join(ws.Root, "widget"),
	}}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repos) != 1 {
		t.Errorf("got %d repos, want the missing one kept: %+v", len(reloaded.Repos), reloaded.Repos)
	}
}

// A plain directory is not a worktree and must not be adopted as one.
func TestLoadIgnoresNonRepoDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root, nil)
	ws := newWorkspace(t, cfg, "feat/thing")
	if err := os.MkdirAll(filepath.Join(ws.Root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repos) != 0 {
		t.Errorf("adopted a non-repo directory: %+v", reloaded.Repos)
	}
}
