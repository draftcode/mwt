// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/draftcode/mwt/internal/git"
)

// newSyncRepo builds an origin whose main has two commits and a clone parked one
// commit behind, which is the state sync exists to resolve.
func newSyncRepo(t *testing.T) (clone, origin string) {
	t.Helper()
	root := t.TempDir()
	origin = filepath.Join(root, "origin")
	clone = filepath.Join(root, "clone")

	mustGit(t, root, "init", "--bare", "--initial-branch=main", origin)
	seed := filepath.Join(root, "seed")
	mustGit(t, root, "clone", origin, seed)
	mustGit(t, seed, "config", "user.email", "test@test.invalid")
	mustGit(t, seed, "config", "user.name", "test")
	mustGit(t, seed, "commit", "--allow-empty", "-m", "base")
	mustGit(t, seed, "push", "-u", "origin", "main")

	mustGit(t, root, "clone", origin, clone)
	mustGit(t, clone, "config", "user.email", "test@test.invalid")
	mustGit(t, clone, "config", "user.name", "test")

	mustGit(t, seed, "commit", "--allow-empty", "-m", "ahead")
	mustGit(t, seed, "push", "origin", "main")
	return clone, origin
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func TestSyncRepoFastForwards(t *testing.T) {
	clone, _ := newSyncRepo(t)

	got := syncRepo(clone, &syncOptions{})

	if got.kind != syncSynced || !strings.Contains(got.detail, "(1)") {
		t.Errorf("want a 1-commit fast-forward, got %+v", got)
	}
	if git.CountCommits(clone, "HEAD..origin/main") != 0 {
		t.Error("checkout is still behind origin/main after sync")
	}
}

func TestSyncRepoUpToDate(t *testing.T) {
	clone, _ := newSyncRepo(t)
	syncRepo(clone, &syncOptions{})

	got := syncRepo(clone, &syncOptions{})

	if got.kind != syncSynced || !strings.Contains(got.detail, "up to date") {
		t.Errorf("want up to date, got %+v", got)
	}
}

// A topic branch checked out in the canonical repo must be left alone: sync only
// advances the default branch, and moving anything else would be a surprise.
func TestSyncRepoSkipsOtherBranch(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "checkout", "-b", "topic", "origin/main")

	got := syncRepo(clone, &syncOptions{})

	if got.kind != syncOtherBranch || !strings.Contains(got.detail, "on topic") {
		t.Errorf("want a skip naming the branch, got %+v", got)
	}
}

func TestSyncRepoSkipsDetachedHead(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "checkout", "--detach", "HEAD")

	got := syncRepo(clone, &syncOptions{})

	if got.kind != syncOtherBranch || !strings.Contains(got.detail, "detached") {
		t.Errorf("want a detached-HEAD skip, got %+v", got)
	}
}

// A checkout carrying its own commits on the default branch cannot fast-forward.
// Sync must report that rather than rewriting the branch.
func TestSyncRepoReportsDivergence(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "commit", "--allow-empty", "-m", "local only")
	local := git.ShortSHA(clone, "HEAD")

	got := syncRepo(clone, &syncOptions{})

	if got.kind != syncFailed {
		t.Errorf("want a failure for a diverged branch, got %+v", got)
	}
	if strings.Contains(got.detail, "\n") {
		t.Errorf("error must be one line or it breaks the column layout: %q", got.detail)
	}
	if git.ShortSHA(clone, "HEAD") != local {
		t.Error("diverged branch was moved")
	}
}

// Untracked files are not a reason to refuse: git carries them across a
// fast-forward untouched.
func TestSyncRepoFastForwardsWithUntrackedFiles(t *testing.T) {
	clone, _ := newSyncRepo(t)
	if err := os.WriteFile(filepath.Join(clone, "scratch.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := syncRepo(clone, &syncOptions{})

	if got.kind != syncSynced || !strings.Contains(got.detail, "(1)") {
		t.Errorf("want a fast-forward despite untracked files, got %+v", got)
	}
	if _, err := os.Stat(filepath.Join(clone, "scratch.log")); err != nil {
		t.Errorf("untracked file did not survive: %v", err)
	}
}

// The fan-out must not let finishing order leak into the table: results are
// reported against the repo they came from, however the goroutines interleave.
func TestSyncAllKeepsInputOrder(t *testing.T) {
	var repos []syncTarget
	for i := range 12 {
		clone, _ := newSyncRepo(t)
		// Give half the repos nothing to do, so the fast ones finish first and
		// would reorder the output if position were not preserved.
		if i%2 == 0 {
			syncRepo(clone, &syncOptions{})
		}
		repos = append(repos, syncTarget{name: fmt.Sprintf("repo%02d", i), path: clone})
	}

	results := syncAll(repos, &syncOptions{}, newProgress(io.Discard, len(repos)))

	if len(results) != len(repos) {
		t.Fatalf("got %d results for %d repos", len(results), len(repos))
	}
	for i := range repos {
		want := "up to date"
		if i%2 != 0 {
			want = "(1)"
		}
		if !strings.Contains(results[i].detail, want) {
			t.Errorf("repo%02d: want %q, got %q", i, want, results[i].detail)
		}
	}
}

// Progress is a terminal affordance; a pipe or a file must stay clean so the
// output can be parsed.
func TestProgressSilentWhenNotTerminal(t *testing.T) {
	var buf bytes.Buffer
	p := newProgress(&buf, 3)

	p.done("one")
	p.done("two")
	p.clear()

	if buf.Len() != 0 {
		t.Errorf("want no output when not a terminal, got %q", buf.String())
	}
}

func TestSyncRepoSkipsRepoWithoutOrigin(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main", dir)

	got := syncRepo(dir, &syncOptions{})

	if got.kind != syncNoRemote {
		t.Errorf("want a no-remote skip, got %+v", got)
	}
}

// --no-fetch must not consult the remote, so a checkout already level with its
// remote-tracking ref reads as up to date even when origin has moved on.
func TestSyncRepoNoFetchSkipsRemote(t *testing.T) {
	clone, _ := newSyncRepo(t)

	got := syncRepo(clone, &syncOptions{noFetch: true})

	if got.kind != syncSynced || !strings.Contains(got.detail, "up to date") {
		t.Errorf("want up to date without fetching, got %+v", got)
	}
}

// The point of the grouping is that a reader sees the categories, not a repo
// list: healthy repos collapse under one heading and the ones needing attention
// sit under their own.
func TestRenderSyncGroupsByCategory(t *testing.T) {
	repos := []syncTarget{{name: "alpha"}, {name: "bravo"}, {name: "charlie"}, {name: "delta"}}
	results := []syncResult{
		{kind: syncSynced, detail: "main\tup to date"},
		{kind: syncOtherBranch, detail: "on topic\twant main"},
		{kind: syncNoRemote},
		{kind: syncSynced, detail: "main\tabc123 -> def456 (2)"},
	}

	var buf bytes.Buffer
	if err := renderSync(&buf, repos, results); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	for _, want := range []string{"up to date / synced (2)", "on another branch (1)", "no origin remote (1)"} {
		if !strings.Contains(got, want) {
			t.Errorf("want heading %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "skipped (") || strings.Contains(got, "failed (") {
		t.Errorf("empty categories must not be printed:\n%s", got)
	}
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "delta") {
		t.Errorf("synced repos are missing:\n%s", got)
	}
	if strings.Index(got, "alpha") > strings.Index(got, "bravo") {
		t.Errorf("synced group must come first:\n%s", got)
	}
}
