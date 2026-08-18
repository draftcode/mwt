// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package ghstack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/draftcode/mwt/internal/git"
)

// repo builds a git repo and returns its path.
func repo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
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

func write(t *testing.T, dir, state string) {
	t.Helper()
	gitDir, err := git.Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, StateFile), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBranchesReadsEveryStack(t *testing.T) {
	dir := repo(t)
	write(t, dir, `{
	  "schemaVersion": 1,
	  "stacks": [
	    {"branches": [
	      {"branch": "feat/base", "pullRequest": {"number": 1, "url": "https://github.com/acme/widget/pull/1"}},
	      {"branch": "feat/top"}
	    ]},
	    {"branches": [{"branch": "fix/other", "pullRequest": {"url": "https://github.com/acme/widget/pull/9"}}]}
	  ]
	}`)

	got, err := Branches(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Branch{
		{Name: "feat/base", PRURL: "https://github.com/acme/widget/pull/1"},
		{Name: "feat/top"},
		{Name: "fix/other", PRURL: "https://github.com/acme/widget/pull/9"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("branch %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A worktree that never ran gh stack has no state file, which is not an error.
func TestBranchesWithoutStateIsEmpty(t *testing.T) {
	got, err := Branches(repo(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

// A schema mwt cannot read must be reported, not silently read as an empty stack.
func TestBranchesRejectsMalformedState(t *testing.T) {
	dir := repo(t)
	write(t, dir, "not json")
	if _, err := Branches(dir); err == nil {
		t.Error("malformed state parsed without error")
	}
}
