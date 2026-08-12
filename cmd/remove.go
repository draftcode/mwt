// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/git"
	"github.com/draftcode/mwt/internal/workspace"
)

func removeCmd() *cobra.Command {
	var opts struct {
		force        bool
		deleteBranch bool
		yes          bool
	}
	cmd := &cobra.Command{
		Use:     "remove <workspace>",
		Aliases: []string{"rm"},
		Short:   "Remove a workspace and all of its worktrees",
		Args:    cobra.ExactArgs(1),

		ValidArgsFunction: completeWorkspaces,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Find(cfg, args[0])
			if err != nil {
				return err
			}

			var blockers []string
			for _, r := range ws.Repos {
				unpushed, reason, err := git.HasUnpushedWork(r.Path)
				if err != nil {
					blockers = append(blockers, fmt.Sprintf("%s: cannot inspect (%v)", r.Name, err))
					continue
				}
				if unpushed {
					blockers = append(blockers, fmt.Sprintf("%s: %s", r.Name, reason))
				}
			}
			if len(blockers) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "workspace %s has work that is not on a remote:\n", ws.Name)
				for _, b := range blockers {
					fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", b)
				}
				if !opts.force {
					return errors.New("refusing to remove; re-run with --force to discard")
				}
			}

			if !opts.yes && !confirm(cmd, fmt.Sprintf("remove %s (%s)?", ws.Root, repoNames(ws))) {
				return errors.New("aborted")
			}

			if err := removeWorkspace(ws, removalOpts{force: opts.force, deleteBranch: opts.deleteBranch}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "removed %s\n", ws.Root)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&opts.force, "force", "f", false, "remove even with uncommitted or unpushed work")
	cmd.Flags().BoolVar(&opts.deleteBranch, "delete-branch", false, "also delete the workspace branch in each repo")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// removalOpts controls how far removeWorkspace goes.
type removalOpts struct {
	force        bool
	deleteBranch bool
	// forceDeleteBranch uses git branch -D. A squash-merged branch is not an
	// ancestor of its base, so -d refuses to delete it even though the work landed.
	forceDeleteBranch bool
}

// removeWorkspace detaches every worktree of a workspace and deletes its root directory.
func removeWorkspace(ws *workspace.Workspace, opts removalOpts) error {
	var errs []error
	for _, r := range ws.Repos {
		// Read the branch while the worktree still exists, and prefer it over the
		// recorded name: the checked-out branch is the one the removal was judged
		// against, and the recorded one may belong to work that is still live.
		branch := ws.Branch
		if s, err := git.Describe(r.Path); err == nil && s.OnBranch() {
			branch = s.Branch
		}
		if err := git.RemoveWorktree(r.Source, r.Path, opts.force); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
			continue
		}
		if opts.deleteBranch {
			flag := "-d"
			if opts.force || opts.forceDeleteBranch {
				flag = "-D"
			}
			if err := git.RunPassthrough(r.Source, "branch", flag, branch); err != nil {
				errs = append(errs, fmt.Errorf("%s: delete branch: %w", r.Name, err))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	// Nothing that still looks like a repo may be deleted as a plain directory:
	// whatever is left was never detached from its source, so removing it would
	// take the work with it and strand the registration.
	if left, err := leftoverRepos(ws.Root); err != nil {
		return err
	} else if len(left) > 0 {
		return fmt.Errorf("refusing to delete %s: unrecognized worktree(s) remain: %s",
			ws.Root, strings.Join(left, ", "))
	}
	return os.RemoveAll(ws.Root)
}

// leftoverRepos names the directories under root that are still git worktrees.
func leftoverRepos(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && git.IsRepo(filepath.Join(root, e.Name())) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func confirm(cmd *cobra.Command, prompt string) bool {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
