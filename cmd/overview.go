// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"sync"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/gh"
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

// prLookups run concurrently because each one is a gh round trip and a dozen
// workspaces would otherwise take a dozen serial network calls.
const prConcurrency = 8

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
					row := overviewRow{Workspace: ws.Name, Repo: r.Name, Path: r.Path, Branch: ws.Branch}
					if s, err := git.Describe(r.Path); err == nil {
						row.Branch, row.Dirty, row.Ahead, row.Behind = s.Branch, s.Dirty, s.Ahead, s.Behind
					}
					rows = append(rows, row)
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

func fillPRs(rows []overviewRow) {
	sem := make(chan struct{}, prConcurrency)
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
	prev := ""
	for _, r := range rows {
		// Print the workspace once per group so multi-repo workspaces read as a
		// unit rather than repeating a long branch name on every line.
		ws := r.Workspace
		if ws == prev {
			ws = ""
		} else {
			prev = r.Workspace
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ws, dash(r.Repo), r.Branch,
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
