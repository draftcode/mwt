// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"os"
	"path/filepath"
	"strings"
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

// addSubmodule wires origin/sub into dir as an initialized submodule and pushes
// the result, so a worktree cut from the branch populates it too.
func addSubmodule(t *testing.T, dir string) {
	t.Helper()
	sub := filepath.Join(t.TempDir(), "sub")
	mustRun(t, dir, "init", "--bare", "--initial-branch=main", sub)
	seed := filepath.Join(t.TempDir(), "seed")
	mustRun(t, dir, "clone", sub, seed)
	mustRun(t, seed, "config", "user.email", "test@test.invalid")
	mustRun(t, seed, "config", "user.name", "test")
	commit(t, seed, "sub base")
	mustRun(t, seed, "push", "-u", "origin", "main")

	mustRun(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", sub, "submodules/dep")
	mustRun(t, dir, "commit", "-m", "add submodule")
}

// worktreeWithSubmodule cuts a worktree and populates its submodule, which is
// what puts the submodule gitdir under the worktree's own admin directory.
func worktreeWithSubmodule(t *testing.T, dir, branch string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	mustRun(t, dir, "worktree", "add", "-b", branch, wt, "HEAD")
	mustRun(t, wt, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")
	return wt
}

// git refuses to remove a worktree with an initialized submodule. The check is
// waved through by its --force, which would discard uncommitted work too, so
// mwt has to clear the worktree itself before forcing.
func TestRemoveWorktreeWithSubmodule(t *testing.T) {
	dir := newRepo(t)
	addSubmodule(t, dir)
	wt := worktreeWithSubmodule(t, dir, "topic")

	if _, err := Run(dir, "worktree", "remove", wt); err == nil {
		t.Fatal("git removed the worktree on its own, the test proves nothing")
	}

	if err := RemoveWorktree(dir, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still present: %v", err)
	}
	if out := mustRun(t, dir, "worktree", "list"); strings.Contains(out, wt) {
		t.Errorf("worktree still registered:\n%s", out)
	}
}

// Forcing past the submodule check would take uncommitted work with it, so an
// unforced removal has to stop instead.
func TestRemoveWorktreeWithSubmoduleKeepsDirtyWork(t *testing.T) {
	dir := newRepo(t)
	addSubmodule(t, dir)
	wt := worktreeWithSubmodule(t, dir, "topic")
	write(t, wt, "uncommitted.txt")

	if err := RemoveWorktree(dir, wt, false); err == nil {
		t.Fatal("RemoveWorktree discarded uncommitted work")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree was removed anyway: %v", err)
	}

	if err := RemoveWorktree(dir, wt, true); err != nil {
		t.Fatalf("RemoveWorktree with force: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still present: %v", err)
	}
}

func writeCommit(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "add", name)
	mustRun(t, dir, "commit", "-m", msg)
}

func unmerged(t *testing.T, dir, prHead string) int {
	t.Helper()
	n, err := UnmergedCommits(dir, prHead)
	if err != nil {
		t.Fatalf("UnmergedCommits: %v", err)
	}
	return n
}

// A branch rebased between its last push and the merge keeps a copy of every
// merged commit under a fresh hash. The PR head is then neither an ancestor of
// HEAD nor on any remote ref, so reachability reads the copy as work that never
// landed, and no later push can ever settle it.
func TestUnmergedCommitsIgnoresRewrittenCopy(t *testing.T) {
	dir := newRepo(t)
	topicBranch(t, dir)
	writeCommit(t, dir, "feature.txt", "feature\n", "work")
	prHead := mustRun(t, dir, "rev-parse", "HEAD")

	mustRun(t, dir, "checkout", "main")
	writeCommit(t, dir, "trunk.txt", "trunk\n", "someone else's work")
	mustRun(t, dir, "push", "origin", "main")
	mustRun(t, dir, "checkout", "topic")
	mustRun(t, dir, "rebase", "origin/main")

	if head := mustRun(t, dir, "rev-parse", "HEAD"); head == prHead {
		t.Fatal("the rebase left the hash unchanged, the test proves nothing")
	}
	if got := unpushed(t, dir, prHead); got != 1 {
		t.Fatalf("UnpushedCommits: got %d, want 1", got)
	}
	if got := unmerged(t, dir, prHead); got != 0 {
		t.Errorf("UnmergedCommits: got %d, want 0", got)
	}
}

// A commit whose content differs from the merged tip is not a copy of it, even
// under the same subject: the local version is missing whatever the push added.
func TestUnmergedCommitsKeepsDivergedCommit(t *testing.T) {
	dir := newRepo(t)
	topicBranch(t, dir)
	writeCommit(t, dir, "feature.txt", "feature\nreviewed\n", "work")
	prHead := mustRun(t, dir, "rev-parse", "HEAD")

	mustRun(t, dir, "reset", "--hard", "HEAD~1")
	writeCommit(t, dir, "feature.txt", "feature\n", "work")

	if got := unmerged(t, dir, prHead); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

// submoduleAt gives the submodule an identity and returns its path.
func submoduleAt(t *testing.T, dir string) string {
	t.Helper()
	sub := filepath.Join(dir, "submodules/dep")
	mustRun(t, sub, "config", "user.email", "test@test.invalid")
	mustRun(t, sub, "config", "user.name", "test")
	return sub
}

// assertDirty guards the negative submodule cases: a clean worktree would pass
// them without the filter ever being asked anything.
func assertDirty(t *testing.T, dir string) {
	t.Helper()
	s, err := Describe(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Dirty == 0 {
		t.Fatal("worktree is clean, the test proves nothing")
	}
}

// The gitlink a merged superproject commit records outlives its own commit once
// the submodule's branch is squash-merged and deleted: nothing can resolve it,
// and the checkout can only sit elsewhere.
func TestStaleSubmodulePointers(t *testing.T) {
	dir := newRepo(t)
	addSubmodule(t, dir)
	sub := submoduleAt(t, dir)

	base := mustRun(t, sub, "rev-parse", "HEAD")
	writeCommit(t, sub, "dep.txt", "dep\n", "submodule work")
	doomed := mustRun(t, sub, "rev-parse", "HEAD")
	mustRun(t, dir, "add", "submodules/dep")
	mustRun(t, dir, "commit", "-m", "bump submodule")

	mustRun(t, sub, "reset", "--hard", base)
	mustRun(t, sub, "reflog", "expire", "--expire=now", "--all")
	mustRun(t, sub, "gc", "--prune=now", "--quiet")
	if HasCommit(sub, doomed) {
		t.Fatal("the recorded commit survived in the submodule, the test proves nothing")
	}

	s, err := Describe(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Dirty != 1 {
		t.Fatalf("Describe reports %d dirty file(s), want 1", s.Dirty)
	}
	stale, err := StaleSubmodulePointers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != "submodules/dep" {
		t.Errorf("got %v, want [submodules/dep]", stale)
	}
}

// A gitlink that still resolves is an ordinary bump: someone can commit it, so it
// is work.
func TestStaleSubmodulePointersKeepsResolvableBump(t *testing.T) {
	dir := newRepo(t)
	addSubmodule(t, dir)
	sub := submoduleAt(t, dir)
	writeCommit(t, sub, "dep.txt", "dep\n", "submodule work")

	assertDirty(t, dir)
	stale, err := StaleSubmodulePointers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("got %v, want none", stale)
	}
}

// Content changed inside the submodule is work no matter what the gitlink does.
func TestStaleSubmodulePointersKeepsSubmoduleEdits(t *testing.T) {
	dir := newRepo(t)
	addSubmodule(t, dir)
	sub := submoduleAt(t, dir)
	write(t, sub, "untracked.txt")

	assertDirty(t, dir)
	stale, err := StaleSubmodulePointers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("got %v, want none", stale)
	}
}

// A submodule holding commits of its own that no remote has is work, and the
// worktree's own gitdir is the only place it lives — an unresolvable gitlink must
// not wave that away.
func TestStaleSubmodulePointersKeepsUnpushedSubmoduleWork(t *testing.T) {
	dir := newRepo(t)
	addSubmodule(t, dir)
	sub := submoduleAt(t, dir)

	base := mustRun(t, sub, "rev-parse", "HEAD")
	writeCommit(t, sub, "dep.txt", "dep\n", "submodule work")
	doomed := mustRun(t, sub, "rev-parse", "HEAD")
	mustRun(t, dir, "add", "submodules/dep")
	mustRun(t, dir, "commit", "-m", "bump submodule")

	mustRun(t, sub, "reset", "--hard", base)
	mustRun(t, sub, "reflog", "expire", "--expire=now", "--all")
	mustRun(t, sub, "gc", "--prune=now", "--quiet")
	if HasCommit(sub, doomed) {
		t.Fatal("the recorded commit survived in the submodule, the test proves nothing")
	}
	writeCommit(t, sub, "later.txt", "later\n", "work nobody pushed")

	assertDirty(t, dir)
	stale, err := StaleSubmodulePointers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("got %v, want none", stale)
	}
}

// staleGitlinkInWorktree leaves the worktree's submodule pointing at a commit the
// submodule no longer has, which is what a merged-and-deleted submodule branch
// does to a superproject commit that recorded its tip.
func staleGitlinkInWorktree(t *testing.T, wt string) {
	t.Helper()
	sub := submoduleAt(t, wt)
	base := mustRun(t, sub, "rev-parse", "HEAD")
	writeCommit(t, sub, "dep.txt", "dep\n", "submodule work")
	doomed := mustRun(t, sub, "rev-parse", "HEAD")
	mustRun(t, wt, "add", "submodules/dep")
	mustRun(t, wt, "commit", "-m", "bump submodule")

	mustRun(t, sub, "reset", "--hard", base)
	mustRun(t, sub, "reflog", "expire", "--expire=now", "--all")
	mustRun(t, sub, "gc", "--prune=now", "--quiet")
	if HasCommit(sub, doomed) {
		t.Fatal("the recorded commit survived in the submodule, the test proves nothing")
	}
}

// An unresolvable gitlink must not stand in for uncommitted work: git's own
// submodule refusal sends removal through the stand-in dirty check, and counting
// the gitlink there strands a worktree nobody can clean up without --force.
func TestRemoveWorktreeWithStaleSubmodulePointer(t *testing.T) {
	dir := newRepo(t)
	addSubmodule(t, dir)
	wt := worktreeWithSubmodule(t, dir, "topic")
	staleGitlinkInWorktree(t, wt)
	assertDirty(t, wt)

	if err := RemoveWorktree(dir, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still present: %v", err)
	}
}

// mwt rm reads the same dirt, and must not refuse over it either.
func TestHasUnpushedWorkIgnoresStaleSubmodulePointer(t *testing.T) {
	dir := newRepo(t)
	addSubmodule(t, dir)
	wt := worktreeWithSubmodule(t, dir, "topic")
	staleGitlinkInWorktree(t, wt)
	mustRun(t, wt, "push", "-u", "origin", "topic")

	has, reason, err := HasUnpushedWork(wt)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Errorf("HasUnpushedWork = true (%s), want false", reason)
	}
}
