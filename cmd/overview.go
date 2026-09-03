// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/gh"
	"github.com/draftcode/mwt/internal/ghstack"
	"github.com/draftcode/mwt/internal/git"
	"github.com/draftcode/mwt/internal/workspace"
)

// overviewRow is one worktree: a repo inside a workspace.
type overviewRow struct {
	Workspace string `json:"workspace"`
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	Dirty     int    `json:"dirty"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	PR        int    `json:"pr,omitempty"`
	State     string `json:"state,omitempty"`
	Checks    string `json:"checks,omitempty"`
	URL       string `json:"url,omitempty"`
}

// Network work fans out because each unit is a round trip — a gh lookup or a git
// fetch — and a few dozen of them would otherwise be a few dozen serial waits.
const maxConcurrency = 8

func overviewCmd() *cobra.Command {
	var noPR bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "List every worktree with its git and pull request state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := workspace.List(cfg)
			if err != nil {
				return err
			}
			if len(all) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "no workspaces under %s\n", cfg.WorktreeRoot)
				return nil
			}

			var rows []overviewRow
			for _, ws := range all {
				// A workspace can have no repos (created then emptied). Emit a
				// placeholder so it is still visible and can be cleaned up.
				if len(ws.Repos) == 0 {
					rows = append(rows, overviewRow{Workspace: ws.Name, Branch: ws.Branch})
					continue
				}
				for _, r := range ws.Repos {
					rows = append(rows, repoRows(ws, r)...)
				}
			}

			if !noPR {
				fillPRs(rows)
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			return renderOverview(cmd, rows)
		},
	}
	cmd.Flags().BoolVar(&noPR, "no-pr", false, "skip pull request lookups (no network, no gh)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return cmd
}

// repoRows expands one worktree into a row per branch of the stack gh stack
// tracks there, base first, and a single row when there is no stack to show.
func repoRows(ws *workspace.Workspace, r workspace.Repo) []overviewRow {
	base := overviewRow{Workspace: ws.Name, Repo: r.Name, Path: r.Path, Branch: ws.Branch}
	current := base
	if s, err := git.Describe(r.Path); err == nil {
		current.Branch, current.Dirty, current.Ahead, current.Behind = s.Branch, s.Dirty, s.Ahead, s.Behind
	}
	stack, err := ghstack.Branches(r.Path)
	if err != nil {
		return []overviewRow{current}
	}
	// gh stack keeps a merged branch in its state after deleting the branch itself,
	// so the state names branches that are no longer there.
	var live []string
	for _, b := range stack {
		if git.BranchExists(r.Path, b.Name) {
			live = append(live, b.Name)
		}
	}
	if len(live) < 2 {
		return []overviewRow{current}
	}

	var rows []overviewRow
	// A worktree can sit on a branch outside its stack, or on none at all; that
	// checkout is the one thing the table must not lose.
	if !slices.Contains(live, current.Branch) {
		rows = append(rows, current)
	}
	for _, name := range live {
		if name == current.Branch {
			rows = append(rows, current)
			continue
		}
		row := base
		row.Branch = name
		row.Ahead, row.Behind = git.BranchAheadBehind(r.Path, name)
		rows = append(rows, row)
	}
	return rows
}

func fillPRs(rows []overviewRow) {
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// A missing PR and an unreachable gh both leave the columns blank;
			// neither is worth failing the table over.
			pr, err := gh.Lookup(rows[i].Path, rows[i].Branch)
			if err != nil || pr == nil {
				return
			}
			rows[i].PR, rows[i].State, rows[i].Checks, rows[i].URL = pr.Number, pr.State, pr.Checks, pr.URL
		}(i)
	}
	wg.Wait()
}

func renderOverview(cmd *cobra.Command, rows []overviewRow) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "WORKSPACE\tREPO\tBRANCH\tD\tA\tB\tPR\tSTATE\tCHECKS")
	prevWS, prevRepo := "", ""
	for _, r := range rows {
		// Print the workspace and repo once per group so a multi-repo workspace and
		// a multi-branch stack each read as a unit rather than repeating a long
		// name on every line.
		ws, repo := r.Workspace, dash(r.Repo)
		if ws == prevWS {
			ws = ""
			if repo == prevRepo {
				repo = ""
			}
		} else {
			prevWS = r.Workspace
		}
		if repo != "" {
			prevRepo = repo
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ws, repo, r.Branch,
			count(r.Dirty), count(r.Ahead), count(r.Behind),
			prNumber(r.PR), dash(r.State), dash(r.Checks))
	}
	return w.Flush()
}

// count renders zero as a dash so a nonzero value stands out in a wall of rows.
func count(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

func prNumber(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("#%d", n)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
