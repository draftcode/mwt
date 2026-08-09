// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

// Package gh looks up pull request state for a worktree via the gh CLI.
//
// gh resolves the repo from the working directory's origin remote, so a worktree
// path is enough — mwt never needs to know the owner/name.
package gh

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
)

// ErrUnavailable means gh is missing or not authenticated, so PR state cannot be
// fetched at all. Callers render an empty column rather than failing.
var ErrUnavailable = errors.New("gh unavailable")

// PR is the pull request opened from a worktree's branch, if there is one.
type PR struct {
	Number int
	State  string // OPEN, MERGED, CLOSED, or DRAFT for an open draft
	Checks string // passing, failing, pending, or "" when no checks ran
	URL    string
}

type prJSON struct {
	Number            int    `json:"number"`
	State             string `json:"state"`
	IsDraft           bool   `json:"isDraft"`
	URL               string `json:"url"`
	StatusCheckRollup []struct {
		// CheckRun reports status+conclusion; StatusContext reports state.
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
}

// Available reports whether the gh binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// Lookup returns the PR for branch as seen from dir, or nil when none exists.
func Lookup(dir, branch string) (*PR, error) {
	if !Available() {
		return nil, ErrUnavailable
	}
	cmd := exec.Command("gh", "pr", "list",
		"--head", branch,
		"--state", "all",
		"--limit", "1",
		"--json", "number,state,isDraft,url,statusCheckRollup",
	)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// No remote, not a GitHub repo, not authenticated — all indistinguishable
		// here and none worth failing the whole table over.
		return nil, ErrUnavailable
	}
	var list []prJSON
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	p := list[0]
	state := p.State
	if p.IsDraft && state == "OPEN" {
		state = "DRAFT"
	}
	return &PR{Number: p.Number, State: state, Checks: rollup(p), URL: p.URL}, nil
}

// rollup collapses the per-check results into one word. Failing wins over
// pending so a broken build is never hidden behind a still-running job.
func rollup(p prJSON) string {
	var pending bool
	for _, c := range p.StatusCheckRollup {
		switch {
		case c.Conclusion == "FAILURE", c.Conclusion == "TIMED_OUT",
			c.Conclusion == "CANCELLED", c.Conclusion == "STARTUP_FAILURE",
			strings.EqualFold(c.State, "FAILURE"), strings.EqualFold(c.State, "ERROR"):
			return "failing"
		case c.Status != "" && c.Status != "COMPLETED",
			strings.EqualFold(c.State, "PENDING"):
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	if len(p.StatusCheckRollup) == 0 {
		return ""
	}
	return "passing"
}
