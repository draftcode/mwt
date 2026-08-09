// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/git"
	"github.com/draftcode/mwt/internal/workspace"
)

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "status [workspace]",
		Short:             "Show per-repo branch and dirty state for a workspace",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorkspaces,
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) == 1 {
				name = args[0]
			}
			ws, err := workspace.Find(cfg, name)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(cmd.OutOrStdout(), "%s  (%s)\n", ws.Name, ws.Root)
			fmt.Fprintln(w, "REPO\tBRANCH\tDIRTY\tAHEAD\tBEHIND")
			for _, r := range ws.Repos {
				s, err := git.Describe(r.Path)
				if err != nil {
					fmt.Fprintf(w, "%s\t?\t?\t?\t?\n", r.Name)
					continue
				}
				upstream := ""
				if !s.HasUpstream {
					upstream = " (no upstream)"
				}
				fmt.Fprintf(w, "%s\t%s%s\t%d\t%d\t%d\n", r.Name, s.Branch, upstream, s.Dirty, s.Ahead, s.Behind)
			}
			return w.Flush()
		},
	}
	return cmd
}

func repoNames(ws *workspace.Workspace) string {
	names := make([]string, 0, len(ws.Repos))
	for _, r := range ws.Repos {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}
