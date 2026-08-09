// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/workspace"
)

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List workspaces",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := workspace.List(cfg)
			if err != nil {
				return err
			}
			if len(all) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "no workspaces under %s\n", cfg.WorktreeRoot)
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "WORKSPACE\tBRANCH\tREPOS\tPATH")
			for _, ws := range all {
				names := make([]string, 0, len(ws.Repos))
				for _, r := range ws.Repos {
					names = append(names, r.Name)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ws.Name, ws.Branch, strings.Join(names, ","), ws.Root)
			}
			return w.Flush()
		},
	}
}
