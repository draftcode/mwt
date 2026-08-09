// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

// Package git wraps the git plumbing mwt needs.
package git

import (
	"fmt"
	"os"
	"os/exec"
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

// RemoveWorktree detaches a worktree from its repo.
func RemoveWorktree(repoDir, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	return RunPassthrough(repoDir, append(args, path)...)
}

// Status summarizes a worktree's state relative to its upstream.
type Status struct {
	Branch      string
	Dirty       int
	Ahead       int
	Behind      int
	HasUpstream bool
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
	switch {
	case s.HasUpstream && s.Ahead > 0:
		reasons = append(reasons, fmt.Sprintf("%d unpushed commit(s)", s.Ahead))
	case !s.HasUpstream:
		base, err := DefaultBase(dir, "origin/HEAD")
		if err == nil {
			if commits, err := Run(dir, "log", "--oneline", base+"..HEAD"); err == nil && commits != "" {
				reasons = append(reasons, fmt.Sprintf("%d commit(s) not on %s", len(strings.Split(commits, "\n")), base))
			}
		}
	}
	return len(reasons) > 0, strings.Join(reasons, ", "), nil
}
