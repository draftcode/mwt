// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/draftcode/mwt/internal/git"
	"github.com/draftcode/mwt/internal/workspace"
)

// A prune that failed on a later step is re-run against what it already removed.
func TestRemoveWorkspaceDeletesRootWhenBranchIsAlreadyGone(t *testing.T) {
	clone, _ := newSyncRepo(t)
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workspace.MetaFile), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{
		Name:   "topic",
		Branch: "topic",
		Root:   root,
		Repos: []workspace.Repo{
			{Name: "repo", Source: clone, Path: filepath.Join(root, "repo")},
		},
	}

	if err := removeWorkspace(ws, removalOpts{deleteBranch: true, forceDeleteBranch: true}); err != nil {
		t.Fatalf("removeWorkspace: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("workspace root still there: %v", err)
	}
}

func TestRemoveWorkspaceDeletesBranchThatOutlivedTheWorktree(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "branch", "topic", "origin/main")
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{
		Name:   "topic",
		Branch: "topic",
		Root:   root,
		Repos: []workspace.Repo{
			{Name: "repo", Source: clone, Path: filepath.Join(root, "repo")},
		},
	}

	if err := removeWorkspace(ws, removalOpts{deleteBranch: true, forceDeleteBranch: true}); err != nil {
		t.Fatalf("removeWorkspace: %v", err)
	}
	if out := mustGit(t, clone, "branch", "--list", "topic"); out != "" {
		t.Errorf("branch survived removal: %q", out)
	}
}

// A repo whose work has landed leaves the workspace while its siblings stay, so
// the worktree, its branch and the record of it all have to go together.
func TestRemoveRepoLeavesTheRestOfTheWorkspace(t *testing.T) {
	done, _ := newSyncRepo(t)
	live, _ := newSyncRepo(t)
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{Name: "topic", Branch: "topic", Root: root}
	for name, source := range map[string]string{"done": done, "live": live} {
		path := filepath.Join(root, name)
		if err := git.AddWorktree(source, path, "topic", "origin/main"); err != nil {
			t.Fatal(err)
		}
		ws.Repos = append(ws.Repos, workspace.Repo{Name: name, Source: source, Path: path})
	}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}
	finished, _ := ws.Repo("done")

	if err := removeRepo(ws, finished, removalOpts{deleteBranch: true, forceDeleteBranch: true}); err != nil {
		t.Fatalf("removeRepo: %v", err)
	}

	if _, err := os.Stat(finished.Path); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	if git.BranchExists(done, "topic") {
		t.Error("branch of the finished repo survived")
	}
	if _, err := os.Stat(filepath.Join(root, "live")); err != nil {
		t.Errorf("sibling worktree was taken too: %v", err)
	}
	// Reloaded from disk: a record left behind would read as a worktree whose
	// directory is gone, and every later command would report it.
	reloaded, err := workspace.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Repo("done"); ok {
		t.Error("removed repo is still recorded")
	}
	if _, ok := reloaded.Repo("live"); !ok {
		t.Error("sibling is no longer recorded")
	}
}
