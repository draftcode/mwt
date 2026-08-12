// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"os"
	"path/filepath"
	"testing"
)

// newRepo builds an origin with one commit on main and a clone of it, and
// returns the clone's path.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	clone := filepath.Join(root, "clone")

	mustRun(t, root, "init", "--bare", "--initial-branch=main", origin)
	mustRun(t, root, "clone", origin, clone)
	mustRun(t, clone, "config", "user.email", "test@test.invalid")
	mustRun(t, clone, "config", "user.name", "test")
	commit(t, clone, "base")
	mustRun(t, clone, "push", "-u", "origin", "main")
	return clone
}

func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	mustRun(t, dir, "commit", "--allow-empty", "-m", msg)
}

// topicBranch cuts a branch the way mwt does, from the remote-tracking base,
// which is what leaves origin/main as the upstream.
func topicBranch(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, dir, "checkout", "-b", "topic", "origin/main")
}

func write(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func unpushed(t *testing.T, dir string, known ...string) int {
	t.Helper()
	n, err := UnpushedCommits(dir, known...)
	if err != nil {
		t.Fatalf("UnpushedCommits: %v", err)
	}
	return n
}

// A branch cut from main keeps main as its upstream, so its ahead count stays
// nonzero after a push. Only reachability from a remote ref settles it.
func TestUnpushedCommitsIgnoresUpstreamBranch(t *testing.T) {
	dir := newRepo(t)
	topicBranch(t, dir)
	commit(t, dir, "work")

	if got := unpushed(t, dir); got != 1 {
		t.Errorf("before push: got %d, want 1", got)
	}

	mustRun(t, dir, "push", "origin", "topic")

	s, err := Describe(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Ahead != 1 {
		t.Fatalf("upstream is expected to still read ahead 1, got %d", s.Ahead)
	}
	if got := unpushed(t, dir); got != 0 {
		t.Errorf("after push: got %d, want 0", got)
	}

	has, reason, err := HasUnpushedWork(dir)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Errorf("HasUnpushedWork = true (%s), want false", reason)
	}
}

// After a squash merge the remote branch is gone and the local commits are on no
// remote ref, but the PR head accounts for them.
func TestUnpushedCommitsCountsKnownRefAsReachable(t *testing.T) {
	dir := newRepo(t)
	topicBranch(t, dir)
	commit(t, dir, "work")
	head := mustRun(t, dir, "rev-parse", "HEAD")

	if got := unpushed(t, dir); got != 1 {
		t.Fatalf("without known ref: got %d, want 1", got)
	}
	if got := unpushed(t, dir, head); got != 0 {
		t.Errorf("with PR head known: got %d, want 0", got)
	}

	commit(t, dir, "work after the PR was opened")
	if got := unpushed(t, dir, head); got != 1 {
		t.Errorf("commit past the PR head: got %d, want 1", got)
	}
}

// A PR head that was never fetched must not abort the count.
func TestUnpushedCommitsSkipsMissingKnownRef(t *testing.T) {
	dir := newRepo(t)
	topicBranch(t, dir)
	commit(t, dir, "work")

	if got := unpushed(t, dir, "0000000000000000000000000000000000000000", ""); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

// The squash-merge shape: the branch tip lives only under refs/pull/<n>/head on
// the remote, so it must be fetched before the count means anything.
func TestFetchPRHeadRecoversMergedTip(t *testing.T) {
	dir := newRepo(t)
	topicBranch(t, dir)
	commit(t, dir, "local work")
	local := mustRun(t, dir, "rev-parse", "HEAD")
	commit(t, dir, "work that only the PR ever saw")
	prHead := mustRun(t, dir, "rev-parse", "HEAD")

	// GitHub keeps a merged PR's tip at refs/pull/<n>/head after the head branch
	// is deleted. Locally it is gone: no branch, no remote-tracking ref, and the
	// object itself collected.
	mustRun(t, dir, "push", "origin", "HEAD:refs/pull/7/head")
	mustRun(t, dir, "reset", "--hard", local)
	mustRun(t, dir, "reflog", "expire", "--expire=now", "--all")
	mustRun(t, dir, "gc", "--prune=now", "--quiet")
	if HasCommit(dir, prHead) {
		t.Fatal("PR head is still present locally, the test proves nothing")
	}

	if got := unpushed(t, dir, prHead); got != 1 {
		t.Fatalf("before fetching the PR head: got %d, want 1", got)
	}
	if err := FetchPRHead(dir, 7); err != nil {
		t.Fatalf("FetchPRHead: %v", err)
	}
	if got := unpushed(t, dir, prHead); got != 0 {
		t.Errorf("after fetching the PR head: got %d, want 0", got)
	}
}

func TestDescribeReportsCheckedOutBranch(t *testing.T) {
	dir := newRepo(t)
	topicBranch(t, dir)
	commit(t, dir, "work")
	mustRun(t, dir, "branch", "-m", "renamed")

	s, err := Describe(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch != "renamed" || !s.OnBranch() {
		t.Errorf("got branch %q OnBranch=%v, want \"renamed\" true", s.Branch, s.OnBranch())
	}

	mustRun(t, dir, "checkout", "--detach")
	s, err = Describe(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.OnBranch() {
		t.Errorf("detached HEAD reported branch %q, want none", s.Branch)
	}
}

func TestHasUnpushedWorkReportsDirtyFiles(t *testing.T) {
	dir := newRepo(t)
	topicBranch(t, dir)
	write(t, dir, "new.txt")

	has, reason, err := HasUnpushedWork(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !has || reason != "1 uncommitted file(s)" {
		t.Errorf("got (%v, %q), want (true, \"1 uncommitted file(s)\")", has, reason)
	}
}
