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

// MainWorktree returns the path of the repo a worktree belongs to.
func MainWorktree(dir string) (string, error) {
	common, err := Run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Dir(common), nil
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
	if s.Dirty > 0 {
		return fmt.Errorf("%s holds %d uncommitted file(s); re-run with --force to discard", path, s.Dirty)
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
	args := []string{"rev-list", "--count", "HEAD"}
	for _, ref := range known {
		// An unknown ref would abort rev-list, and a PR head is routinely absent
		// locally (never fetched, or dropped when the remote branch was deleted).
		if !HasCommit(dir, ref) {
			continue
		}
		args = append(args, "^"+ref)
	}
	// --not must come last: it flips the sense of every ref after it, so the ^refs
	// above would turn into inclusions if they trailed it.
	args = append(args, "--not", "--remotes")
	out, err := Run(dir, args...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// HasUnpushedWork reports whether the worktree has local changes that would be lost.
func HasUnpushedWork(dir string) (bool, string, error) {
	s, err := Describe(dir)
	if err != nil {
		return false, "", err
	}
	var reasons []string
	if s.Dirty > 0 {
		reasons = append(reasons, fmt.Sprintf("%d uncommitted file(s)", s.Dirty))
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
