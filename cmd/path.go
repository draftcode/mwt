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
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	head, err := gh.LookupHead(dir, ref)
	if err != nil {
		return err
	}
	matches, err := workspace.FindByBranch(cfg, head.Branch, head.Repo)
	if err != nil {
		return err
	}
	switch len(matches) {
	case 1:
		fmt.Fprintln(cmd.OutOrStdout(), matches[0].Path)
		return nil
	case 0:
		return fmt.Errorf("no %s worktree on branch %s under %s", head.Repo, head.Branch, cfg.WorktreeRoot)
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "branch %s is checked out in %d worktrees:", head.Branch, len(matches))
		for _, m := range matches {
			fmt.Fprintf(&b, "\n  %s\t(workspace %s)", m.Path, m.Workspace)
		}
		return errors.New(b.String())
	}
}
