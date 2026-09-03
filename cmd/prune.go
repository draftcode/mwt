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
	"github.com/draftcode/mwt/internal/ghstack"
	"github.com/draftcode/mwt/internal/git"
	"github.com/draftcode/mwt/internal/workspace"
)

// repoVerdict is one repo's answer to "is this worktree done with?".
type repoVerdict struct {
	repo   workspace.Repo
	branch string
	merged bool
	detail string
	// stack holds the repo's stack branches whose pull request merged, other than
	// the one the worktree has checked out.
	stack []branchVerdict
}

// branchVerdict is one stack branch's answer to "has this branch's work landed?".
type branchVerdict struct {
	name   string
	merged bool
	detail string
}

// deletable names the stack branches safe to delete.
func (v repoVerdict) deletable() []branchVerdict {
	var out []branchVerdict
	for _, b := range v.stack {
		if b.merged {
			out = append(out, b)
		}
	}
	return out
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

// staleBranches counts the stack branches the workspace would give up.
func (v wsVerdict) staleBranches() int {
	n := 0
	for _, r := range v.repos {
		n += len(r.deletable())
	}
	return n
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
			if !opts.keepBranch {
				judgeStacks(verdicts)
			}
			var doomed, kept []wsVerdict
			for _, v := range verdicts {
				if v.prunable() {
					doomed = append(doomed, v)
					continue
				}
				if v.staleBranches() > 0 {
					kept = append(kept, v)
				}
			}
			reportPrune(cmd, verdicts)
			if len(doomed) == 0 && len(kept) == 0 {
				return nil
			}
			if opts.dryRun {
				return nil
			}
			if !opts.yes && !confirm(cmd, prunePrompt(doomed, kept)) {
				return errors.New("aborted")
			}

			var errs []error
			for _, v := range doomed {
				if err := removeWorkspace(v.ws, removalOpts{deleteBranch: !opts.keepBranch, forceDeleteBranch: true}); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", v.ws.Name, err))
					continue
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "removed %s\n", v.ws.Root)
				errs = append(errs, deleteStale(cmd, v)...)
			}
			for _, v := range kept {
				errs = append(errs, deleteStale(cmd, v)...)
			}
			return errors.Join(errs...)
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "only report what would be removed")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&opts.keepBranch, "keep-branch", false, "keep every branch in each source repo")
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

// goneVerdict judges a repo whose worktree directory is no longer there — removed
// by hand, or by a prune that failed on a later repo of the same workspace. The
// files are already gone, so the only thing a removal can still take is the branch
// the worktree left behind in the source repo.
func goneVerdict(r workspace.Repo, recorded string) repoVerdict {
	v := repoVerdict{repo: r, detail: "worktree directory is gone"}
	if !git.BranchExists(r.Source, recorded) {
		v.merged, v.detail = true, "worktree and branch already gone"
		return v
	}
	n, err := git.BranchUnpushedCommits(r.Source, recorded)
	if err != nil {
		v.detail += fmt.Sprintf(", and %s cannot be inspected (%v)", recorded, err)
		return v
	}
	if n > 0 {
		v.detail += fmt.Sprintf(", and %s holds %d unpushed commit(s)", recorded, n)
		return v
	}
	v.merged, v.detail = true, fmt.Sprintf("worktree directory is gone, %s fully pushed", recorded)
	return v
}

func judgeRepo(r workspace.Repo, recorded string) repoVerdict {
	v := repoVerdict{repo: r}

	if _, err := os.Stat(r.Path); err != nil {
		return goneVerdict(r, recorded)
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
	v.branch = branch
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
	// merge was a squash or the branch was rewritten before it. Only work that never
	// reached the PR still matters: uncommitted files, and commits whose patch is
	// nowhere upstream.
	unsaved, stale := git.UnsavedFiles(r.Path, s.Dirty)
	if unsaved > 0 {
		v.detail = fmt.Sprintf("PR #%d merged%s, but %d uncommitted file(s)", pr.Number, suffix, unsaved)
		return v
	}
	unpushed, err := unmergedAgainst(r.Path, "HEAD", pr)
	if err != nil {
		v.detail = fmt.Sprintf("PR #%d merged%s, but cannot inspect (%v)", pr.Number, suffix, err)
		return v
	}
	if unpushed > 0 {
		v.detail = fmt.Sprintf("PR #%d merged%s, but %d commit(s) diverge from the merged tip", pr.Number, suffix, unpushed)
		return v
	}
	v.merged, v.detail = true, fmt.Sprintf("PR #%d merged%s", pr.Number, suffix)
	if stale > 0 {
		v.detail += fmt.Sprintf(" (%d stale submodule pointer(s))", stale)
	}
	return v
}

// unmergedAgainst counts what a merged pull request leaves unaccounted for on ref.
//
// A squash merge leaves the branch's commits on no remote ref, and deleting the
// head branch takes the PR head with it, so the first count is inflated by work
// that did land. refs/pull/<n>/head still holds that commit: fetch it once and ask
// again, rather than keeping merged work forever.
func unmergedAgainst(dir, ref string, pr *gh.PR) (int, error) {
	n, err := git.UnmergedCommits(dir, ref, pr.HeadOid)
	if err != nil || n == 0 || git.HasCommit(dir, pr.HeadOid) {
		return n, err
	}
	if err := git.FetchPRHead(dir, pr.Number); err != nil {
		return n, nil
	}
	return git.UnmergedCommits(dir, ref, pr.HeadOid)
}

// judgeStacks fills in the stack verdicts of every workspace, in parallel: each
// branch costs a gh round trip, and a stack a dozen deep would otherwise be a
// dozen serial calls.
func judgeStacks(verdicts []wsVerdict) {
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for i := range verdicts {
		for j := range verdicts[i].repos {
			wg.Add(1)
			go func(i, j int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				verdicts[i].repos[j].stack = judgeStack(verdicts[i].repos[j])
			}(i, j)
		}
	}
	wg.Wait()
}

// judgeStack judges every branch of the repo's stack other than the one the
// worktree has checked out, and reports only the ones whose pull request merged.
func judgeStack(v repoVerdict) []branchVerdict {
	stack, err := ghstack.Branches(v.repo.Path)
	if err != nil {
		return nil
	}
	var out []branchVerdict
	for _, b := range stack {
		if b.Name == v.branch || !git.BranchExists(v.repo.Source, b.Name) {
			continue
		}
		if bv, ok := judgeBranch(v.repo, b.Name); ok {
			out = append(out, bv)
		}
	}
	return out
}

func judgeBranch(r workspace.Repo, branch string) (branchVerdict, bool) {
	pr, err := gh.Lookup(r.Path, branch)
	if err != nil || pr == nil || pr.State != "MERGED" {
		return branchVerdict{}, false
	}
	v := branchVerdict{name: branch, detail: fmt.Sprintf("PR #%d merged", pr.Number)}
	if path, ok := git.BranchCheckout(r.Source, branch); ok {
		v.detail += fmt.Sprintf(", but checked out at %s", path)
		return v, true
	}
	// Nothing has it checked out, so its own ref is the only history to judge.
	unmerged, err := unmergedAgainst(r.Path, branch, pr)
	if err != nil {
		v.detail += fmt.Sprintf(", but cannot inspect (%v)", err)
		return v, true
	}
	if unmerged > 0 {
		v.detail += fmt.Sprintf(", but %d commit(s) diverge from the merged tip", unmerged)
		return v, true
	}
	v.merged = true
	return v, true
}

// deleteStale deletes the merged stack branches of one workspace from the repos
// that own them.
func deleteStale(cmd *cobra.Command, v wsVerdict) []error {
	var errs []error
	for _, r := range v.repos {
		for _, b := range r.deletable() {
			if err := git.DeleteBranch(r.repo.Source, b.name); err != nil {
				errs = append(errs, fmt.Errorf("%s: delete branch %s: %w", r.repo.Name, b.name, err))
				continue
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted %s (%s)\n", b.name, r.repo.Name)
		}
	}
	return errs
}

func prunePrompt(doomed, kept []wsVerdict) string {
	branches := 0
	for _, v := range doomed {
		branches += v.staleBranches()
	}
	for _, v := range kept {
		branches += v.staleBranches()
	}
	switch {
	case len(doomed) == 0:
		return fmt.Sprintf("delete %d merged branch(es)?", branches)
	case branches == 0:
		return fmt.Sprintf("remove %d workspace(s)?", len(doomed))
	}
	return fmt.Sprintf("remove %d workspace(s) and delete %d merged branch(es)?", len(doomed), branches)
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
			for _, b := range r.stack {
				fmt.Fprintf(w, "\t\t%s: %s\n", b.name, b.detail)
			}
		}
	}
	w.Flush()

	shown = reportStaleBranches(cmd, kept, shown) || shown

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
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s (%s): %s\n", v.ws.Name, r.repo.Name, r.detail)
			for _, b := range r.stack {
				if !b.merged {
					fmt.Fprintf(cmd.ErrOrStderr(), "  %s (%s): %s: %s\n", v.ws.Name, r.repo.Name, b.name, b.detail)
				}
			}
		}
	}
}

// reportStaleBranches lists the merged branches of workspaces that stay, which the
// table above says nothing about because their workspace is not being removed.
func reportStaleBranches(cmd *cobra.Command, kept []wsVerdict, afterTable bool) bool {
	shown := false
	for _, v := range kept {
		for _, r := range v.repos {
			for _, b := range r.deletable() {
				if !shown {
					if afterTable {
						fmt.Fprintln(cmd.ErrOrStderr())
					}
					fmt.Fprintln(cmd.ErrOrStderr(), "merged branches to delete:")
					shown = true
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s (%s): %s: %s\n", v.ws.Name, r.repo.Name, b.name, b.detail)
			}
		}
	}
	return shown
}
