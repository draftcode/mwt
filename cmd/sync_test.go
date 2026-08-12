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

	if !strings.Contains(got, "(1)") {
		t.Errorf("want a 1-commit fast-forward, got %q", got)
	}
	if git.CountCommits(clone, "HEAD..origin/main") != 0 {
		t.Error("checkout is still behind origin/main after sync")
	}
}

func TestSyncRepoUpToDate(t *testing.T) {
	clone, _ := newSyncRepo(t)
	syncRepo(clone, &syncOptions{})

	got := syncRepo(clone, &syncOptions{})

	if !strings.Contains(got, "up to date") {
		t.Errorf("want up to date, got %q", got)
	}
}

// A topic branch checked out in the canonical repo must be left alone: sync only
// advances the default branch, and moving anything else would be a surprise.
func TestSyncRepoSkipsOtherBranch(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "checkout", "-b", "topic", "origin/main")

	got := syncRepo(clone, &syncOptions{})

	if !strings.Contains(got, "on topic") {
		t.Errorf("want a skip naming the branch, got %q", got)
	}
}

func TestSyncRepoSkipsDetachedHead(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "checkout", "--detach", "HEAD")

	got := syncRepo(clone, &syncOptions{})

	if !strings.Contains(got, "detached") {
		t.Errorf("want a detached-HEAD skip, got %q", got)
	}
}

// A checkout carrying its own commits on the default branch cannot fast-forward.
// Sync must report that rather than rewriting the branch.
func TestSyncRepoReportsDivergence(t *testing.T) {
	clone, _ := newSyncRepo(t)
	mustGit(t, clone, "commit", "--allow-empty", "-m", "local only")
	local := git.ShortSHA(clone, "HEAD")

	got := syncRepo(clone, &syncOptions{})

	if !strings.HasPrefix(got, "error") {
		t.Errorf("want an error for a diverged branch, got %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("error must be one line or it breaks the column layout: %q", got)
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

	if !strings.Contains(got, "(1)") {
		t.Errorf("want a fast-forward despite untracked files, got %q", got)
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

	lines := syncAll(repos, &syncOptions{}, newProgress(io.Discard, len(repos)))

	if len(lines) != len(repos) {
		t.Fatalf("got %d lines for %d repos", len(lines), len(repos))
	}
	for i := range repos {
		want := "up to date"
		if i%2 != 0 {
			want = "(1)"
		}
		if !strings.Contains(lines[i], want) {
			t.Errorf("repo%02d: want %q, got %q", i, want, lines[i])
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

	if !strings.Contains(got, "no origin remote") {
		t.Errorf("want a no-remote skip, got %q", got)
	}
}

// --no-fetch must not consult the remote, so a checkout already level with its
// remote-tracking ref reads as up to date even when origin has moved on.
func TestSyncRepoNoFetchSkipsRemote(t *testing.T) {
	clone, _ := newSyncRepo(t)

	got := syncRepo(clone, &syncOptions{noFetch: true})

	if !strings.Contains(got, "up to date") {
		t.Errorf("want up to date without fetching, got %q", got)
	}
}
