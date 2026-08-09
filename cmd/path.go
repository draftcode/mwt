// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/workspace"
)

func pathCmd() *cobra.Command {
	return &cobra.Command{
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
}
