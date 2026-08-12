// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

// Package cmd implements the mwt command line.
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/config"
)

var cfg *config.Config

// Root builds the mwt command tree.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "mwt",
		Short:         "Manage sets of git worktrees spanning multiple repos",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load()
			if err != nil {
				return err
			}
			cfg = c
			return nil
		},
	}
	root.AddCommand(newCmd(), addCmd(), listCmd(), overviewCmd(), statusCmd(), syncCmd(), removeCmd(), pruneCmd(), pathCmd(), startCmd(), configCmd())
	return root
}
