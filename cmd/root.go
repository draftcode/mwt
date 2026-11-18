// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

// Package cmd implements the mwt command line.
package cmd

import (
	"os"

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
			if err := clearInheritedRepoEnv(); err != nil {
				return err
			}
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

// inheritedRepoEnv are the variables git exports to a `!` alias run inside a
// worktree; left set, they pin every git and gh call to that one repo.
var inheritedRepoEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_PREFIX"}

func clearInheritedRepoEnv() error {
	for _, k := range inheritedRepoEnv {
		if err := os.Unsetenv(k); err != nil {
			return err
		}
	}
	return nil
}
