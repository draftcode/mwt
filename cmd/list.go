// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/workspace"
)

// listEntry is the machine-readable form of a workspace, including the root
// path that the on-disk metadata leaves implicit.
type listEntry struct {
	Name    string           `json:"name"`
	Branch  string           `json:"branch"`
	Root    string           `json:"root"`
	Created time.Time        `json:"created"`
	Repos   []workspace.Repo `json:"repos"`
}

func listCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List workspaces",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := workspace.List(cfg)
			if err != nil {
				return err
			}
			if asJSON {
				entries := make([]listEntry, 0, len(all))
				for _, ws := range all {
					repos := ws.Repos
					if repos == nil {
						repos = []workspace.Repo{}
					}
					entries = append(entries, listEntry{
						Name:    ws.Name,
						Branch:  ws.Branch,
						Root:    filepath.Clean(ws.Root),
						Created: ws.Created,
						Repos:   repos,
					})
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(entries)
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
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print workspaces as JSON")
	return cmd
}
