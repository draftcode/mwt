// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/draftcode/mwt/internal/workspace"
)

// goneRepo names a worktree path that was never created, so every verdict comes
// from the source repo's branch alone.
func goneRepo(t *testing.T, source string) workspace.Repo {
	t.Helper()
	return workspace.Repo{Name: "repo", Source: source, Path: filepath.Join(t.TempDir(), "gone")}
}

// A prune that fails partway through a workspace leaves the repos it already
// finished with no directory. Judging that as a blocker would keep the workspace
// forever, and there is nothing left in it to keep.
func TestGoneVerdictPrunesWhenBranchIsPushed(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "checkout", "-b", "topic", "origin/main")
	mustGit(t, clone, "push", "-u", "origin", "topic")
	mustGit(t, clone, "checkout", "main")

	v := goneVerdict(goneRepo(t, clone), "topic")

	if !v.merged {
		t.Errorf("verdict is not prunable: %s", v.detail)
	}
}

func TestGoneVerdictPrunesWhenBranchIsGoneToo(t *testing.T) {
	clone, _ := newSyncRepo(t)

	v := goneVerdict(goneRepo(t, clone), "topic")

	if !v.merged {
		t.Errorf("verdict is not prunable: %s", v.detail)
	}
}

// The branch outliving the worktree is the one thing a removal can still take.
func TestGoneVerdictKeepsUnpushedBranch(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "checkout", "-b", "topic", "origin/main")
	mustGit(t, clone, "commit", "--allow-empty", "-m", "work")
	mustGit(t, clone, "checkout", "main")

	v := goneVerdict(goneRepo(t, clone), "topic")

	if v.merged {
		t.Error("verdict discarded a branch with unpushed commits")
	}
	if !strings.Contains(v.detail, "1 unpushed commit(s)") {
		t.Errorf("detail does not name the blocker: %s", v.detail)
	}
}
