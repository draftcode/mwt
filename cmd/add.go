// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/workspace"
)

func addCmd() *cobra.Command {
	var opts struct {
		name    string
		base    string
		noFetch bool
		noSetup bool
	}
	cmd := &cobra.Command{
		Use:               "add <repo...>",
		Short:             "Add repos to an existing workspace",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeReposToAdd,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Find(cfg, opts.name)
			if err != nil {
				return err
			}
			return workspace.AddRepos(cfg, ws, args, workspace.AddOptions{
				Base:    opts.base,
				Fetch:   !opts.noFetch,
				NoSetup: opts.noSetup,
				Out:     cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVarP(&opts.name, "workspace", "w", "", "workspace name (default: inferred from cwd)")
	cmd.Flags().StringVar(&opts.base, "base", "", "base ref to branch from (default: origin/HEAD per repo)")
	cmd.Flags().BoolVar(&opts.noFetch, "no-fetch", false, "skip git fetch before branching")
	cmd.Flags().BoolVar(&opts.noSetup, "no-setup", false, "skip configured setup commands")
	cmd.RegisterFlagCompletionFunc("workspace", completeWorkspaces)
	cmd.RegisterFlagCompletionFunc("base", noCompletion)
	return cmd
}
