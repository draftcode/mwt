// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"testing"

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
