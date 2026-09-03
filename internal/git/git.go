// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

// Package git wraps the git plumbing mwt needs.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Run executes git in dir and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// RunPassthrough executes git in dir with stdout/stderr attached to the terminal.
func RunPassthrough(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	out, err := Run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// DefaultBase resolves the preferred base ref, falling back when origin/HEAD is unset.
func DefaultBase(dir, want string) (string, error) {
	if want != "" && want != "origin/HEAD" {
		return want, nil
	}
	if ref, err := Run(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return ref, nil
	}
	for _, candidate := range []string{"origin/main", "origin/master"} {
		if _, err := Run(dir, "rev-parse", "--verify", "--quiet", candidate); err == nil {
			return candidate, nil
		}
	}
	head, err := Run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("cannot determine base ref: %w", err)
	}
	return head, nil
}

// RemoteExists reports whether dir has a remote of that name.
func RemoteExists(dir, remote string) bool {
	out, err := Run(dir, "remote")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == remote {
			return true
		}
	}
	return false
}

// Fetch updates remote-tracking refs for remote, pruning deleted branches.
func Fetch(dir, remote string) error {
	_, err := Run(dir, "fetch", "--quiet", "--prune", remote)
	return err
}

// DefaultBranch returns the branch remote/HEAD points at, without the remote prefix.
func DefaultBranch(dir, remote string) (string, error) {
	ref, err := Run(dir, "symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return "", fmt.Errorf("%s/HEAD is unset; run git remote set-head %s -a: %w", remote, remote, err)
	}
	return strings.TrimPrefix(ref, remote+"/"), nil
}

// FastForward advances the current branch to ref, refusing anything but a fast-forward.
func FastForward(dir, ref string) error {
	_, err := Run(dir, "merge", "--ff-only", "--quiet", ref)
	return err
}

// ShortSHA resolves ref to its abbreviated commit hash.
func ShortSHA(dir, ref string) string {
	out, err := Run(dir, "rev-parse", "--short", ref)
	if err != nil {
		return "?"
	}
	return out
}

// CountCommits returns how many commits are in the range spec, e.g. "a..b".
func CountCommits(dir, spec string) int {
	out, err := Run(dir, "rev-list", "--count", spec)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0
	}
	return n
}

// MainWorktree returns the path of the repo a worktree belongs to.
func MainWorktree(dir string) (string, error) {
	common, err := Run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Dir(common), nil
}

// Dir returns the git directory backing a worktree, which for a linked worktree
// is its own directory under the repo's worktrees/, not the shared common dir.
func Dir(dir string) (string, error) {
	return Run(dir, "rev-parse", "--path-format=absolute", "--git-dir")
}

// BranchExists reports whether a local branch of that name exists.
func BranchExists(dir, branch string) bool {
	_, err := Run(dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// AddWorktree creates a worktree at path, creating branch from base when needed.
func AddWorktree(repoDir, path, branch, base string) error {
	args := []string{"worktree", "add"}
	if BranchExists(repoDir, branch) {
		args = append(args, path, branch)
	} else {
		args = append(args, "-b", branch, path, base)
	}
	return RunPassthrough(repoDir, args...)
}

// submoduleRefusal is how git declines to remove a worktree holding an
// initialized submodule: it will not try to clean up the submodule's separate
// gitdir. The check sits behind git's own --force, alongside the dirty and
// locked ones, so the only way past it is to force the removal.
const submoduleRefusal = "working trees containing submodules cannot be moved or removed"

// RemoveWorktree detaches a worktree from its repo.
func RemoveWorktree(repoDir, path string, force bool) error {
	_, err := Run(repoDir, removeArgs(path, force)...)
	if err == nil || force || !strings.Contains(err.Error(), submoduleRefusal) {
		return err
	}

	// Forcing past the submodule check would also wave through uncommitted work,
	// which the caller did not ask for, so stand in for the check git skips.
	s, describeErr := Describe(path)
	if describeErr != nil {
		return fmt.Errorf("cannot inspect %s: %w", path, describeErr)
	}
	if unsaved, _ := UnsavedFiles(path, s.Dirty); unsaved > 0 {
		return fmt.Errorf("%s holds %d uncommitted file(s); re-run with --force to discard", path, unsaved)
	}
	_, err = Run(repoDir, removeArgs(path, true)...)
	return err
}

func removeArgs(path string, force bool) []string {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	return append(args, path)
}

// DetachedHead is what git status reports as the branch when HEAD is detached.
const DetachedHead = "(detached)"

// Status summarizes a worktree's state relative to its upstream.
type Status struct {
	Branch      string
	Dirty       int
	Ahead       int
	Behind      int
	HasUpstream bool
}

// OnBranch reports whether the worktree has a branch checked out.
func (s Status) OnBranch() bool {
	return s.Branch != "" && s.Branch != DetachedHead
}

// Describe collects branch, dirty-file count and ahead/behind counts for a worktree.
func Describe(dir string) (Status, error) {
	var s Status
	out, err := Run(dir, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "# branch.head "):
			s.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			s.HasUpstream = true
			fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(fields) == 2 {
				s.Ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[0], "+"))
				s.Behind, _ = strconv.Atoi(strings.TrimPrefix(fields[1], "-"))
			}
		case strings.HasPrefix(line, "#"):
		default:
			s.Dirty++
		}
	}
	return s, nil
}

// HasCommit reports whether ref names a commit object present in dir.
func HasCommit(dir, ref string) bool {
	if ref == "" {
		return false
	}
	_, err := Run(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// FetchPRHead fetches the tip GitHub recorded for a pull request. Once the head
// branch is deleted, refs/pull/<n>/head is the only place that commit survives,
// and without it a squash-merged branch cannot be told from work never pushed.
func FetchPRHead(dir string, number int) error {
	_, err := Run(dir, "fetch", "--quiet", "origin", fmt.Sprintf("refs/pull/%d/head", number))
	return err
}

// UnpushedCommits counts commits reachable from HEAD that no remote-tracking ref
// contains, treating each ref in known as reachable too.
//
// This deliberately ignores the branch's upstream. A branch cut from origin/main
// tracks origin/main for its whole life, so its ahead count is just "commits on
// this branch" — pushing the branch to origin/<branch> never brings it back to
// zero, and every branch with a commit reads as unpushed.
func UnpushedCommits(dir string, known ...string) (int, error) {
	commits, err := UnpushedCommitList(dir, known...)
	return len(commits), err
}

// UnpushedCommitList is UnpushedCommits with the commits themselves, newest first.
func UnpushedCommitList(dir string, known ...string) ([]string, error) {
	return refUnpushedCommitList(dir, "HEAD", known)
}

func refUnpushedCommitList(dir, ref string, known []string) ([]string, error) {
	args := []string{"rev-list", ref}
	for _, k := range known {
		// An unknown ref would abort rev-list, and a PR head is routinely absent
		// locally (never fetched, or dropped when the remote branch was deleted).
		if !HasCommit(dir, k) {
			continue
		}
		args = append(args, "^"+k)
	}
	// --not must come last: it flips the sense of every ref after it, so the ^refs
	// above would turn into inclusions if they trailed it.
	args = append(args, "--not", "--remotes")
	out, err := Run(dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// BranchUnpushedCommits counts commits on branch that no remote-tracking ref
// holds. It answers for a branch what UnpushedCommits answers for a checked-out
// worktree, which is all that is left to judge once the worktree is gone.
func BranchUnpushedCommits(dir, branch string) (int, error) {
	out, err := Run(dir, "rev-list", "--count", branch, "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// PruneWorktrees drops the repo's records of worktrees whose directory is gone.
func PruneWorktrees(dir string) error {
	_, err := Run(dir, "worktree", "prune")
	return err
}

// EquivalentCommits returns the commits between upstream and head whose patch
// upstream already holds under a different hash. git cherry does the patch-id
// comparison and marks such a commit "-".
func EquivalentCommits(dir, upstream, head string) (map[string]bool, error) {
	out, err := Run(dir, "cherry", upstream, head)
	if err != nil {
		return nil, err
	}
	dup := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		if mark, sha, ok := strings.Cut(line, " "); ok && mark == "-" {
			dup[sha] = true
		}
	}
	return dup, nil
}

// UnmergedCommits counts the commits on ref that a merged pull request does not
// account for: those on no remote ref whose patch is absent from prHead's history
// too. ref is "HEAD" for a checked-out branch and a branch name for any other,
// which is every branch of a stack but one.
//
// Matching by patch and not by hash is what makes the count survive a rewrite. A
// branch amended or rebased between its last push and the merge keeps a copy of
// every merged commit under a fresh hash, and by reachability alone each copy
// reads as work that never landed — permanently, since no later push can make a
// commit that no longer exists upstream reachable.
func UnmergedCommits(dir, ref, prHead string) (int, error) {
	local, err := refUnpushedCommitList(dir, ref, []string{prHead})
	if err != nil {
		return 0, err
	}
	if len(local) == 0 || !HasCommit(dir, prHead) {
		return len(local), nil
	}
	dup, err := EquivalentCommits(dir, prHead, ref)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range local {
		if !dup[c] {
			n++
		}
	}
	return n, nil
}

// UnsavedFiles splits a dirty-file count into the files that hold work and the
// submodule gitlinks nothing can resolve. Every caller that asks "would removing
// this worktree lose something?" wants the first number, and the second only to
// say why the count moved.
func UnsavedFiles(dir string, dirty int) (unsaved, stale int) {
	if dirty == 0 {
		return 0, 0
	}
	paths, err := StaleSubmodulePointers(dir)
	if err != nil {
		return dirty, 0
	}
	return dirty - len(paths), len(paths)
}

// StaleSubmodulePointers lists the dirty paths that are submodule gitlinks whose
// recorded commit is missing from the submodule.
//
// A squash merge deletes the submodule branch the superproject commit pointed at,
// which takes the recorded commit with it: nothing can restore that gitlink and
// the checkout can only sit on some other commit. The dirt is unresolvable rather
// than unsaved work. A submodule holding edited content, untracked files, or
// commits of its own that no remote has is left out — that is work, and a missing
// gitlink cannot tell a deleted branch from one this clone never fetched.
func StaleSubmodulePointers(dir string) ([]string, error) {
	out, err := Run(dir, "status", "--porcelain=v2")
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, line := range strings.Split(out, "\n") {
		// "1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>", where sub reads S<c><m><u>
		// for a submodule and hH is the commit HEAD records for it. Renamed entries
		// ("2 ") carry a tab-separated pair of paths and never describe a gitlink bump.
		if !strings.HasPrefix(line, "1 ") {
			continue
		}
		fields := strings.SplitN(line, " ", 9)
		if len(fields) < 9 || fields[2] != "SC.." {
			continue
		}
		sub := filepath.Join(dir, fields[8])
		if !IsRepo(sub) || HasCommit(sub, fields[6]) {
			continue
		}
		if n, err := UnpushedCommits(sub); err != nil || n > 0 {
			continue
		}
		stale = append(stale, fields[8])
	}
	return stale, nil
}

// HasUnpushedWork reports whether the worktree has local changes that would be lost.
func HasUnpushedWork(dir string) (bool, string, error) {
	s, err := Describe(dir)
	if err != nil {
		return false, "", err
	}
	var reasons []string
	if unsaved, _ := UnsavedFiles(dir, s.Dirty); unsaved > 0 {
		reasons = append(reasons, fmt.Sprintf("%d uncommitted file(s)", unsaved))
	}
	n, err := UnpushedCommits(dir)
	if err != nil {
		return false, "", err
	}
	if n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d unpushed commit(s)", n))
	}
	return len(reasons) > 0, strings.Join(reasons, ", "), nil
}

// BranchAheadBehind counts commits between branch and its upstream, for a branch
// that need not be checked out. Both counts are zero when there is no upstream.
func BranchAheadBehind(dir, branch string) (ahead, behind int) {
	out, err := Run(dir, "rev-list", "--left-right", "--count", branch+"..."+branch+"@{upstream}")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0
	}
	ahead, _ = strconv.Atoi(fields[0])
	behind, _ = strconv.Atoi(fields[1])
	return ahead, behind
}

// BranchCheckout returns the worktree holding branch, if any worktree of the repo
// has it checked out. Deleting such a branch is something git refuses, so asking
// first keeps a removal from being reported before it is attempted.
func BranchCheckout(dir, branch string) (string, bool) {
	out, err := Run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}
	path := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch:
			return path, true
		}
	}
	return "", false
}

// DeleteBranch drops a local branch, with -D so a squash-merged one goes too: it
// is not an ancestor of its base, and -d refuses it even though the work landed.
func DeleteBranch(dir, branch string) error {
	_, err := Run(dir, "branch", "-D", branch)
	return err
}
