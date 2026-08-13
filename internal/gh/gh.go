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
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// ErrUnavailable means gh is missing or not authenticated, so PR state cannot be
// fetched at all. Callers render an empty column rather than failing.
var ErrUnavailable = errors.New("gh unavailable")

// PR is the pull request opened from a worktree's branch, if there is one.
type PR struct {
	Number  int
	State   string // OPEN, MERGED, CLOSED, or DRAFT for an open draft
	Checks  string // passing, failing, pending, or "" when no checks ran
	URL     string
	HeadOid string // commit the PR points at, used to tell merged work from stray work
}

type prJSON struct {
	Number            int    `json:"number"`
	State             string `json:"state"`
	IsDraft           bool   `json:"isDraft"`
	URL               string `json:"url"`
	HeadRefOid        string `json:"headRefOid"`
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
		"--json", "number,state,isDraft,url,headRefOid,statusCheckRollup",
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
	return &PR{Number: p.Number, State: state, Checks: rollup(p), URL: p.URL, HeadOid: p.HeadRefOid}, nil
}

// Ref identifies a pull request. Repo is "owner/name", or empty when the
// reference carried no repo and gh must infer one from the working directory.
type Ref struct {
	Repo   string
	Number int
}

var prURL = regexp.MustCompile(`^(?:https?://)?[^/\s]*github\.com/([^/\s]+/[^/\s]+)/pull/(\d+)`)

// ParseRef reads a pull request URL, an owner/name#number pair, or a bare number.
// Trailing URL segments such as /files or a #discussion anchor are ignored.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if m := prURL.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 {
			return Ref{}, fmt.Errorf("pull request number in %q is not a positive integer", s)
		}
		return Ref{Repo: m[1], Number: n}, nil
	}
	if repo, num, ok := strings.Cut(s, "#"); ok && strings.Count(repo, "/") == 1 {
		if n, err := strconv.Atoi(num); err == nil && n > 0 {
			return Ref{Repo: repo, Number: n}, nil
		}
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(s, "#")); err == nil && n > 0 {
		return Ref{Number: n}, nil
	}
	return Ref{}, fmt.Errorf("cannot read %q as a pull request URL or number", s)
}

// Head is the branch a pull request was opened from, and the short name of the
// repo holding it.
type Head struct {
	Branch string
	Repo   string
}

// LookupHead resolves a pull request to the branch it was opened from, running gh
// in dir so a ref without a repo can still be inferred from the checkout there.
func LookupHead(dir string, ref Ref) (Head, error) {
	if !Available() {
		return Head{}, ErrUnavailable
	}
	args := []string{"pr", "view", strconv.Itoa(ref.Number), "--json", "headRefName,headRepository"}
	if ref.Repo != "" {
		args = append(args, "--repo", ref.Repo)
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return Head{}, fmt.Errorf("gh pr view %d: %s", ref.Number, strings.TrimSpace(string(exit.Stderr)))
		}
		return Head{}, fmt.Errorf("gh pr view %d: %w", ref.Number, err)
	}
	var view struct {
		HeadRefName    string `json:"headRefName"`
		HeadRepository struct {
			Name string `json:"name"`
		} `json:"headRepository"`
	}
	if err := json.Unmarshal(out, &view); err != nil {
		return Head{}, err
	}
	if view.HeadRefName == "" {
		return Head{}, fmt.Errorf("pull request %d reports no head branch", ref.Number)
	}
	// The head repo is the fork for a cross-repo pull request; worktrees follow the
	// repo the pull request targets.
	name := view.HeadRepository.Name
	if ref.Repo != "" {
		name = path.Base(ref.Repo)
	}
	return Head{Branch: view.HeadRefName, Repo: name}, nil
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
