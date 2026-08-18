// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

// Package ghstack reads the state the gh stack extension keeps per worktree.
//
// The state lists every branch of a stack along with the pull request opened from
// it, so a pull request maps to a worktree without asking GitHub and without
// depending on which branch of the stack happens to be checked out.
package ghstack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/draftcode/mwt/internal/git"
)

// StateFile is where gh stack stores its state, inside the worktree's git directory.
const StateFile = "gh-stack"

// Branch is one branch of a stack. PRURL is empty until the branch is submitted.
type Branch struct {
	Name  string
	PRURL string
}

type stateJSON struct {
	Stacks []struct {
		Branches []struct {
			Branch      string `json:"branch"`
			PullRequest *struct {
				URL string `json:"url"`
			} `json:"pullRequest"`
		} `json:"branches"`
	} `json:"stacks"`
}

// Branches returns every branch gh stack tracks for the worktree at dir, and none
// when the worktree has no stack.
func Branches(dir string) ([]Branch, error) {
	gitDir, err := git.Dir(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(gitDir, StateFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state stateJSON
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []Branch
	for _, s := range state.Stacks {
		for _, b := range s.Branches {
			e := Branch{Name: b.Branch}
			if b.PullRequest != nil {
				e.PRURL = b.PullRequest.URL
			}
			out = append(out, e)
		}
	}
	return out, nil
}
