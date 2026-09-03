// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/draftcode/mwt/internal/ghstack"
	"github.com/draftcode/mwt/internal/git"
	"github.com/draftcode/mwt/internal/workspace"
)

// stackRepo builds a repo holding branches base and top, with checkout checked
// out, and records base..top as the gh stack unless stack is empty.
func stackRepo(t *testing.T, checkout string, stack ...string) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main", dir)
	mustGit(t, dir, "config", "user.email", "test@test.invalid")
	mustGit(t, dir, "config", "user.name", "test")
	mustGit(t, dir, "commit", "--allow-empty", "-m", "base")
	for _, b := range append([]string{}, stack...) {
		mustGit(t, dir, "branch", b)
	}
	if checkout != "main" && !git.BranchExists(dir, checkout) {
		mustGit(t, dir, "branch", checkout)
	}
	mustGit(t, dir, "checkout", checkout)
	if len(stack) > 0 {
		writeStackState(t, dir, stack)
	}
	return dir
}

func writeStackState(t *testing.T, dir string, branches []string) {
	t.Helper()
	gitDir, err := git.Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]any, 0, len(branches))
	for _, b := range branches {
		entries = append(entries, map[string]any{"branch": b})
	}
	data, err := json.Marshal(map[string]any{"stacks": []any{map[string]any{"branches": entries}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, ghstack.StateFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func rowsFor(t *testing.T, dir string) []overviewRow {
	t.Helper()
	ws := &workspace.Workspace{Name: "feat/base", Branch: "feat/base"}
	return repoRows(ws, workspace.Repo{Name: "widget", Path: dir})
}

func branches(rows []overviewRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Branch)
	}
	return out
}

func TestRepoRowsExpandsTheStackBaseFirst(t *testing.T) {
	dir := stackRepo(t, "feat/base", "feat/base", "feat/top")

	got := branches(rowsFor(t, dir))

	want := []string{"feat/base", "feat/top"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("branches = %v, want %v", got, want)
	}
}

// Dirty files belong to the checkout, so no other branch of the stack may claim them.
func TestRepoRowsCountsDirtyFilesOnTheCheckoutOnly(t *testing.T) {
	dir := stackRepo(t, "feat/top", "feat/base", "feat/top")
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, r := range rowsFor(t, dir) {
		want := 0
		if r.Branch == "feat/top" {
			want = 1
		}
		if r.Dirty != want {
			t.Errorf("%s dirty = %d, want %d", r.Branch, r.Dirty, want)
		}
	}
}

func TestRepoRowsWithoutAStackIsOneRow(t *testing.T) {
	dir := stackRepo(t, "feat/base")

	got := branches(rowsFor(t, dir))

	if len(got) != 1 || got[0] != "feat/base" {
		t.Errorf("branches = %v, want [feat/base]", got)
	}
}

// A single-branch stack says nothing the checkout does not, so it must not turn
// the repo into a group.
func TestRepoRowsWithASingleBranchStackIsOneRow(t *testing.T) {
	dir := stackRepo(t, "feat/base", "feat/base")

	if got := branches(rowsFor(t, dir)); len(got) != 1 {
		t.Errorf("branches = %v, want one row", got)
	}
}

// The checkout is the one row the table must not lose, even when the worktree has
// wandered off its own stack.
func TestRepoRowsKeepsACheckoutOutsideTheStack(t *testing.T) {
	dir := stackRepo(t, "fix/elsewhere", "feat/base", "feat/top")

	got := branches(rowsFor(t, dir))

	want := []string{"fix/elsewhere", "feat/base", "feat/top"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("branches = %v, want %v", got, want)
	}
}

func TestRenderOverviewBlanksARepeatedRepo(t *testing.T) {
	rows := []overviewRow{
		{Workspace: "feat/base", Repo: "widget", Branch: "feat/base"},
		{Workspace: "feat/base", Repo: "widget", Branch: "feat/top"},
		{Workspace: "feat/base", Repo: "gadget", Branch: "feat/base"},
	}
	cmd := overviewCmd()
	var out strings.Builder
	cmd.SetOut(&out)

	if err := renderOverview(cmd, rows); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a header and three rows, got %q", out.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[2]), "feat/top") {
		t.Errorf("repeated repo not blanked: %q", lines[2])
	}
	if !strings.Contains(lines[3], "gadget") {
		t.Errorf("second repo of the workspace not printed: %q", lines[3])
	}
}

// gh stack keeps its metadata when it prunes a merged branch, and so does mwt, so
// the state routinely names branches that no longer exist.
func TestRepoRowsSkipsABranchThatIsGone(t *testing.T) {
	dir := stackRepo(t, "feat/base", "feat/base", "feat/merged", "feat/top")
	mustGit(t, dir, "branch", "-D", "feat/merged")

	got := branches(rowsFor(t, dir))

	want := []string{"feat/base", "feat/top"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("branches = %v, want %v", got, want)
	}
}

// Once the rest of a stack is pruned, the checkout is all that is left and the
// repo must read as a plain one-branch row again.
func TestRepoRowsCollapsesWhenOnlyTheCheckoutIsLeft(t *testing.T) {
	dir := stackRepo(t, "feat/base", "feat/base", "feat/merged")
	mustGit(t, dir, "branch", "-D", "feat/merged")

	if got := branches(rowsFor(t, dir)); len(got) != 1 || got[0] != "feat/base" {
		t.Errorf("branches = %v, want [feat/base]", got)
	}
}
