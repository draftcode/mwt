// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/workspace"
)

func startCmd() *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:                   "start [workspace] [-- claude args...]",
		Short:                 "Launch a claude session rooted at the workspace with every repo added",
		Args:                  cobra.ArbitraryArgs,
		DisableFlagsInUseLine: true,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveDefault
			}
			return completeWorkspaces(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			var passthrough []string
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				if dash > 1 {
					return fmt.Errorf("expected at most one workspace name before --")
				}
				if dash == 1 {
					name = args[0]
				}
				passthrough = args[dash:]
			} else if len(args) > 0 {
				name = args[0]
				passthrough = args[1:]
			}

			ws, err := workspace.Find(cfg, name)
			if err != nil {
				return err
			}
			if len(ws.Repos) == 0 {
				return fmt.Errorf("workspace %s has no repos; run `mwt add <repo>` first", ws.Name)
			}

			argv := append([]string{}, cfg.ClaudeCommand...)
			for _, r := range ws.Repos {
				argv = append(argv, "--add-dir", r.Path)
			}
			argv = append(argv, passthrough...)

			if printOnly {
				fmt.Fprintf(cmd.OutOrStdout(), "cd %s && %s\n", ws.Root, shellJoin(argv))
				return nil
			}

			bin, err := exec.LookPath(argv[0])
			if err != nil {
				return fmt.Errorf("cannot find %s: %w", argv[0], err)
			}
			if err := os.Chdir(ws.Root); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "starting claude in %s with %s\n", ws.Root, repoNames(ws))
			env := append(os.Environ(), "MWT_WORKSPACE="+ws.Name, "MWT_WORKSPACE_ROOT="+ws.Root, "MWT_BRANCH="+ws.Branch)
			return syscall.Exec(bin, argv, env)
		},
	}
	cmd.Flags().BoolVarP(&printOnly, "print", "p", false, "print the command instead of running it")
	return cmd
}

func shellJoin(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
