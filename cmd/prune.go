// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/gh"
	"github.com/draftcode/mwt/internal/git"
	"github.com/draftcode/mwt/internal/workspace"
)

// repoVerdict is one repo's answer to "is this worktree done with?".
type repoVerdict struct {
	repo   workspace.Repo
	merged bool
	detail string
}

// wsVerdict aggregates the repo verdicts: a workspace goes only when every repo does.
type wsVerdict struct {
	ws    *workspace.Workspace
	repos []repoVerdict
}

func (v wsVerdict) prunable() bool {
	if len(v.repos) == 0 {
		return false
	}
	for _, r := range v.repos {
		if !r.merged {
			return false
		}
	}
	return true
}

func pruneCmd() *cobra.Command {
	var opts struct {
		dryRun     bool
		yes        bool
		keepBranch bool
	}
	cmd := &cobra.Command{
		Use:     "prune",
		Aliases: []string{"gc"},
		Short:   "Remove workspaces whose branch is merged in every repo",
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
			if !gh.Available() {
				return errors.New("prune needs the gh CLI to read pull request state")
			}

			verdicts := judge(all)
			var doomed []wsVerdict
			for _, v := range verdicts {
				if v.prunable() {
					doomed = append(doomed, v)
				}
			}
			reportPrune(cmd, verdicts)
			if len(doomed) == 0 {
				return nil
			}
			if opts.dryRun {
				return nil
			}
			if !opts.yes && !confirm(cmd, fmt.Sprintf("remove %d workspace(s)?", len(doomed))) {
				return errors.New("aborted")
			}

			var errs []error
			for _, v := range doomed {
				if err := removeWorkspace(v.ws, removalOpts{deleteBranch: !opts.keepBranch, forceDeleteBranch: true}); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", v.ws.Name, err))
					continue
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "removed %s\n", v.ws.Root)
			}
			return errors.Join(errs...)
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "only report what would be removed")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&opts.keepBranch, "keep-branch", false, "keep the workspace branch in each source repo")
	return cmd
}

// judge inspects every repo of every workspace, in parallel: each verdict costs
// a gh round trip, and a dozen workspaces would otherwise be a dozen serial calls.
func judge(all []*workspace.Workspace) []wsVerdict {
	out := make([]wsVerdict, len(all))
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for i, ws := range all {
		out[i] = wsVerdict{ws: ws, repos: make([]repoVerdict, len(ws.Repos))}
		for j, r := range ws.Repos {
			wg.Add(1)
			go func(i, j int, r workspace.Repo) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				out[i].repos[j] = judgeRepo(r, ws.Branch)
			}(i, j, r)
		}
	}
	wg.Wait()
	return out
}

func judgeRepo(r workspace.Repo, recorded string) repoVerdict {
	v := repoVerdict{repo: r}

	if _, err := os.Stat(r.Path); err != nil {
		v.detail = "worktree directory is gone"
		return v
	}
	// The worktree decides which branch's PR to read, not the name recorded at
	// creation: a branch switched or renamed inside the worktree leaves that name
	// stale, and a merged PR found under the stale name would condemn live work.
	s, err := git.Describe(r.Path)
	if err != nil {
		v.detail = fmt.Sprintf("cannot inspect (%v)", err)
		return v
	}
	if !s.OnBranch() {
		v.detail = "detached HEAD"
		return v
	}
	branch := s.Branch
	// Naming the branch keeps a surprising verdict traceable when the worktree has
	// drifted from the workspace it lives in.
	suffix := ""
	if branch != recorded {
		suffix = fmt.Sprintf(" on %s", branch)
	}

	pr, err := gh.Lookup(r.Path, branch)
	switch {
	case err != nil:
		v.detail = "no pull request state (gh unavailable here)" + suffix
		return v
	case pr == nil:
		v.detail = "no pull request" + suffix
		return v
	case pr.State != "MERGED":
		v.detail = fmt.Sprintf("PR #%d %s%s", pr.Number, pr.State, suffix)
		return v
	}

	// The PR is merged, so commits on the branch are accounted for even when the
	// merge was a squash. Only work that never reached the PR still matters:
	// uncommitted files, and commits pushed nowhere. Counting the PR head as
	// reachable is what keeps a squash-merged branch prunable — its commits live
	// on under no remote ref once the remote branch is deleted.
	if s.Dirty > 0 {
		v.detail = fmt.Sprintf("PR #%d merged%s, but %d uncommitted file(s)", pr.Number, suffix, s.Dirty)
		return v
	}
	unpushed, err := git.UnpushedCommits(r.Path, pr.HeadOid)
	if err != nil {
		v.detail = fmt.Sprintf("PR #%d merged%s, but cannot inspect (%v)", pr.Number, suffix, err)
		return v
	}
	// A squash merge leaves the branch's commits on no remote ref, and deleting the
	// head branch takes the PR head with it, so the count above is inflated by work
	// that did land. refs/pull/<n>/head still holds that commit: fetch it once and
	// ask again, rather than keeping a merged workspace forever.
	if unpushed > 0 && !git.HasCommit(r.Path, pr.HeadOid) {
		if err := git.FetchPRHead(r.Path, pr.Number); err == nil {
			unpushed, err = git.UnpushedCommits(r.Path, pr.HeadOid)
			if err != nil {
				v.detail = fmt.Sprintf("PR #%d merged%s, but cannot inspect (%v)", pr.Number, suffix, err)
				return v
			}
		}
	}
	if unpushed > 0 {
		v.detail = fmt.Sprintf("PR #%d merged%s, but %d unpushed commit(s)", pr.Number, suffix, unpushed)
		return v
	}
	v.merged, v.detail = true, fmt.Sprintf("PR #%d merged%s", pr.Number, suffix)
	return v
}

func reportPrune(cmd *cobra.Command, verdicts []wsVerdict) {
	w := tabwriter.NewWriter(cmd.ErrOrStderr(), 0, 0, 2, ' ', 0)
	var kept []wsVerdict
	shown := false
	for _, v := range verdicts {
		if !v.prunable() {
			kept = append(kept, v)
			continue
		}
		if !shown {
			fmt.Fprintln(w, "TO REMOVE\tREPO\tSTATUS")
			shown = true
		}
		name := v.ws.Name
		for _, r := range v.repos {
			fmt.Fprintf(w, "%s\t%s\t%s\n", name, r.repo.Name, r.detail)
			name = ""
		}
	}
	w.Flush()

	if len(kept) == 0 {
		return
	}
	if shown {
		fmt.Fprintln(cmd.ErrOrStderr())
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "kept:")
	for _, v := range kept {
		if len(v.repos) == 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s: no repos checked out\n", v.ws.Name)
			continue
		}
		for _, r := range v.repos {
			if !r.merged {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s (%s): %s\n", v.ws.Name, r.repo.Name, r.detail)
			}
		}
	}
}
