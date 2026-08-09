// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/workspace"
)

func newCmd() *cobra.Command {
	var opts struct {
		branch  string
		base    string
		noFetch bool
		noSetup bool
	}
	cmd := &cobra.Command{
		Use:   "new <workspace> [repo...]",
		Short: "Create a workspace and check out one worktree per repo",
		Args:  cobra.MinimumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return noCompletion(cmd, args, toComplete)
			}
			return completeSourceRepos(cmd, args[1:], toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			root := filepath.Join(cfg.WorktreeRoot, workspace.DirName(name))
			if _, err := os.Stat(root); err == nil {
				return fmt.Errorf("workspace %q already exists at %s", name, root)
			}
			branch := opts.branch
			if branch == "" {
				branch = name
			}
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			ws := &workspace.Workspace{Name: name, Branch: branch, Root: root, Created: time.Now()}
			if err := ws.Save(); err != nil {
				return err
			}
			if err := workspace.AddRepos(cfg, ws, args[1:], workspace.AddOptions{
				Base:    opts.base,
				Fetch:   !opts.noFetch,
				NoSetup: opts.noSetup,
				Out:     cmd.ErrOrStderr(),
			}); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), root)
			return nil
		},
	}
	cmd.Flags().StringVarP(&opts.branch, "branch", "b", "", "branch name to create in each repo (default: workspace name)")
	cmd.Flags().StringVar(&opts.base, "base", "", "base ref to branch from (default: origin/HEAD per repo)")
	cmd.Flags().BoolVar(&opts.noFetch, "no-fetch", false, "skip git fetch before branching")
	cmd.Flags().BoolVar(&opts.noSetup, "no-setup", false, "skip configured setup commands")
	cmd.RegisterFlagCompletionFunc("branch", noCompletion)
	cmd.RegisterFlagCompletionFunc("base", noCompletion)
	return cmd
}
