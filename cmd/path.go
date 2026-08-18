// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/gh"
	"github.com/draftcode/mwt/internal/workspace"
)

func pathCmd() *cobra.Command {
	var pr string
	cmd := &cobra.Command{
		Use:   "path [workspace] [repo]",
		Short: "Print a workspace or repo path (for shell cd helpers)",
		Args:  cobra.MaximumNArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completeWorkspaces(cmd, args, toComplete)
			}
			return completeWorkspaceRepos(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if pr != "" {
				if len(args) > 0 {
					return errors.New("--pr resolves the path on its own; drop the workspace and repo arguments")
				}
				return pathForPR(cmd, pr)
			}
			var name string
			if len(args) >= 1 {
				name = args[0]
			}
			ws, err := workspace.Find(cfg, name)
			if err != nil {
				return err
			}
			if len(args) == 2 {
				r, ok := ws.Repo(args[1])
				if !ok {
					return fmt.Errorf("repo %q is not in workspace %s", args[1], ws.Name)
				}
				fmt.Fprintln(cmd.OutOrStdout(), r.Path)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), filepath.Clean(ws.Root))
			return nil
		},
	}
	cmd.Flags().StringVar(&pr, "pr", "", "print the worktree path for a pull request URL or number")
	return cmd
}

// pathForPR prints the worktree holding the branch a pull request was opened from.
func pathForPR(cmd *cobra.Command, arg string) error {
	ref, err := gh.ParseRef(arg)
	if err != nil {
		return err
	}
	var matches []workspace.Match
	// A bare number names no repo, so matching it against recorded pull requests
	// would collide across repos; that case takes the gh route, which resolves the
	// repo from the working directory.
	if ref.Repo != "" {
		if matches, err = workspace.FindByPR(cfg, ref); err != nil {
			return err
		}
	}
	subject := fmt.Sprintf("pull request %d", ref.Number)
	if len(matches) == 0 {
		// Nothing recorded locally: the stack state may predate the pull request, or
		// the branch may not be stacked at all. Ask GitHub for its branch instead.
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		head, err := gh.LookupHead(dir, ref)
		if err != nil {
			return err
		}
		if matches, err = workspace.FindByBranch(cfg, head.Branch, head.Repo); err != nil {
			return err
		}
		subject = fmt.Sprintf("branch %s", head.Branch)
	}
	matches = preferCheckedOut(matches)
	switch len(matches) {
	case 1:
		m := matches[0]
		fmt.Fprintln(cmd.OutOrStdout(), m.Path)
		if m.CheckedOut != m.Branch {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: %s has %s checked out, not %s; run `gh stack checkout %d` there\n",
				m.Repo, m.CheckedOut, m.Branch, ref.Number)
		}
		return nil
	case 0:
		return fmt.Errorf("no worktree for %s under %s", subject, cfg.WorktreeRoot)
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%s is in %d worktrees:", subject, len(matches))
		for _, m := range matches {
			fmt.Fprintf(&b, "\n  %s\t(workspace %s)", m.Path, m.Workspace)
		}
		return errors.New(b.String())
	}
}

// preferCheckedOut narrows a set of matches to the worktrees actually sitting on
// the branch. A branch appears in every stack built on top of it, so the workspace
// it was created in is told apart from the ones merely stacked above by which one
// has it checked out.
func preferCheckedOut(matches []workspace.Match) []workspace.Match {
	var on []workspace.Match
	for _, m := range matches {
		if m.CheckedOut == m.Branch {
			on = append(on, m)
		}
	}
	if len(on) == 1 {
		return on
	}
	return matches
}
