// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/draftcode/mwt/internal/git"
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

// stackVerdict builds a workspace verdict whose single repo carries the given
// stack branches, without asking gh anything.
func stackVerdict(t *testing.T, source string, prunable bool, stack ...branchVerdict) wsVerdict {
	t.Helper()
	return wsVerdict{
		ws: &workspace.Workspace{Name: "feat/base", Branch: "feat/base"},
		repos: []repoVerdict{{
			repo:   workspace.Repo{Name: "widget", Source: source, Path: filepath.Join(t.TempDir(), "widget")},
			branch: "feat/base",
			merged: prunable,
			detail: "PR #1 merged",
			stack:  stack,
		}},
	}
}

func TestDeletableSkipsABranchThatCannotGo(t *testing.T) {
	v := stackVerdict(t, "", false,
		branchVerdict{name: "feat/landed", merged: true, detail: "PR #2 merged"},
		branchVerdict{name: "feat/diverged", detail: "PR #3 merged, but 1 commit(s) diverge from the merged tip"},
	)

	got := v.repos[0].deletable()

	if len(got) != 1 || got[0].name != "feat/landed" {
		t.Errorf("deletable = %+v, want feat/landed alone", got)
	}
}

func TestDeleteStaleDeletesOnlyTheMergedBranch(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "branch", "feat/landed")
	mustGit(t, clone, "branch", "feat/diverged")
	v := stackVerdict(t, clone, true,
		branchVerdict{name: "feat/landed", merged: true, detail: "PR #2 merged"},
		branchVerdict{name: "feat/diverged", detail: "PR #3 merged, but 1 commit(s) diverge from the merged tip"},
	)
	cmd := pruneCmd()
	cmd.SetErr(&strings.Builder{})

	if errs := deleteStale(cmd, v); len(errs) > 0 {
		t.Fatalf("deleteStale: %v", errs)
	}

	if git.BranchExists(clone, "feat/landed") {
		t.Error("merged branch survived")
	}
	if !git.BranchExists(clone, "feat/diverged") {
		t.Error("branch with diverging work was deleted")
	}
}

func TestPrunePromptCountsBothKinds(t *testing.T) {
	doomed := stackVerdict(t, "", true, branchVerdict{name: "feat/landed", merged: true})
	kept := stackVerdict(t, "", false, branchVerdict{name: "feat/other", merged: true})

	cases := []struct {
		name   string
		doomed []wsVerdict
		kept   []wsVerdict
		want   string
	}{
		{"workspaces only", []wsVerdict{stackVerdict(t, "", true)}, nil, "remove 1 workspace(s)?"},
		{"branches only", nil, []wsVerdict{kept}, "delete 1 merged branch(es)?"},
		{"both", []wsVerdict{doomed}, []wsVerdict{kept}, "remove 1 workspace(s) and delete 2 merged branch(es)?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := prunePrompt(c.doomed, c.kept); got != c.want {
				t.Errorf("prunePrompt = %q, want %q", got, c.want)
			}
		})
	}
}

// A workspace that stays is not in the removal table, so its merged branches need
// a section of their own or the prompt would offer to delete something unnamed.
func TestReportPruneNamesMergedBranchesOfKeptWorkspaces(t *testing.T) {
	v := stackVerdict(t, "", false,
		branchVerdict{name: "feat/landed", merged: true, detail: "PR #2 merged"},
		branchVerdict{name: "feat/diverged", detail: "PR #3 merged, but 1 commit(s) diverge from the merged tip"},
	)
	v.repos[0].detail = "PR #1 OPEN"
	cmd := pruneCmd()
	var out strings.Builder
	cmd.SetErr(&out)

	reportPrune(cmd, []wsVerdict{v})

	got := out.String()
	if !strings.Contains(got, "merged branches to delete:") || !strings.Contains(got, "feat/landed") {
		t.Errorf("merged branch not offered:\n%s", got)
	}
	if !strings.Contains(got, "kept:") || !strings.Contains(got, "diverge from the merged tip") {
		t.Errorf("blocked branch not explained:\n%s", got)
	}
}

// A workspace kept for one unfinished repo says nothing about the repos that are
// done, which is where the reader learns what the workspace is still waiting on.
func TestReportPruneNamesTheFinishedReposOfAKeptWorkspace(t *testing.T) {
	v := stackVerdict(t, "", false)
	v.repos[0].detail = "PR #1 merged"
	v.repos = append(v.repos, repoVerdict{
		repo:   workspace.Repo{Name: "gadget"},
		branch: "feat/base",
		detail: "no pull request",
	})
	cmd := pruneCmd()
	var out strings.Builder
	cmd.SetErr(&out)

	reportPrune(cmd, []wsVerdict{v})

	got := out.String()
	if !strings.Contains(got, "(widget): PR #1 merged") {
		t.Errorf("finished repo not named:\n%s", got)
	}
	if !strings.Contains(got, "(gadget): no pull request") {
		t.Errorf("blocking repo not named:\n%s", got)
	}
}
