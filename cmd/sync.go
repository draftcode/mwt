// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/config"
	"github.com/draftcode/mwt/internal/git"
	"github.com/draftcode/mwt/internal/workspace"
)

const syncRemote = "origin"

type syncOptions struct {
	noFetch bool
}

func syncCmd() *cobra.Command {
	opts := &syncOptions{}
	cmd := &cobra.Command{
		Use:   "sync [repo...]",
		Short: "Fetch canonical checkouts and fast-forward their default branch",
		Long: `Fetch every canonical checkout under the repo search paths and fast-forward
its default branch to the remote.

Only the canonical checkout is touched, never a workspace worktree: topic
branches belong to whoever is working on them. A repo is skipped when its
checkout is not on the default branch, and the fast-forward is left to git so a
diverged or dirty checkout is reported rather than overwritten.`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeSourceRepos,
		RunE: func(cmd *cobra.Command, args []string) error {
			repos, err := syncTargets(args)
			if err != nil {
				return err
			}
			results := syncAll(repos, opts, newProgress(cmd.ErrOrStderr(), len(repos)))

			if err := renderSync(cmd.OutOrStdout(), repos, results); err != nil {
				return err
			}
			failed := 0
			for _, r := range results {
				if r.kind == syncFailed {
					failed++
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d repo(s) could not be synced", failed)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.noFetch, "no-fetch", false, "skip git fetch and use the remote-tracking refs as they are")
	return cmd
}

// syncKind is the outcome category a repo lands in, which is also the order the
// groups are printed in.
type syncKind int

const (
	syncSynced syncKind = iota
	syncOtherBranch
	syncNoRemote
	syncSkipped
	syncFailed
)

var syncHeadings = map[syncKind]string{
	syncSynced:      "up to date / synced",
	syncOtherBranch: "on another branch",
	syncNoRemote:    "no origin remote",
	syncSkipped:     "skipped",
	syncFailed:      "failed",
}

// syncResult is one repo's outcome. detail holds the tab-separated columns
// printed after the repo name, and is empty when the heading says it all.
type syncResult struct {
	kind   syncKind
	detail string
}

// syncAll runs the repos concurrently and returns their results in the order
// given, so the output reads the same whatever order they finish in. Every repo
// is its own git dir, so the fetches do not contend; the run is network-bound,
// which is what the fan-out buys.
func syncAll(repos []syncTarget, opts *syncOptions, p *progress) []syncResult {
	results := make([]syncResult, len(repos))
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for i, r := range repos {
		wg.Add(1)
		go func(i int, r syncTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = syncRepo(r.path, opts)
			p.done(r.name)
		}(i, r)
	}
	wg.Wait()
	p.clear()
	return results
}

// renderSync prints one section per outcome category, each column-aligned on its
// own so a long error message does not pad the rows of a healthy group.
func renderSync(out io.Writer, repos []syncTarget, results []syncResult) error {
	first := true
	for _, kind := range []syncKind{syncSynced, syncOtherBranch, syncNoRemote, syncSkipped, syncFailed} {
		var idx []int
		for i, r := range results {
			if r.kind == kind {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			continue
		}
		if !first {
			fmt.Fprintln(out)
		}
		first = false
		fmt.Fprintf(out, "%s (%d)\n", syncHeadings[kind], len(idx))
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, i := range idx {
			if results[i].detail == "" {
				fmt.Fprintf(w, "  %s\n", repos[i].name)
				continue
			}
			fmt.Fprintf(w, "  %s\t%s\n", repos[i].name, results[i].detail)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return nil
}

type syncTarget struct {
	name string
	path string
}

// syncTargets resolves the named repos, or discovers every canonical checkout
// when no name is given.
func syncTargets(args []string) ([]syncTarget, error) {
	if len(args) > 0 {
		out := make([]syncTarget, 0, len(args))
		for _, a := range args {
			name, path, err := workspace.ResolveSource(cfg, a)
			if err != nil {
				return nil, err
			}
			out = append(out, syncTarget{name: name, path: path})
		}
		return out, nil
	}

	var out []syncTarget
	seen := map[string]bool{}
	for name, r := range cfg.Repos {
		if r.Path == "" {
			continue
		}
		if p := config.Expand(r.Path); git.IsRepo(p) && !seen[p] {
			seen[p] = true
			out = append(out, syncTarget{name: name, path: p})
		}
	}
	for _, base := range cfg.RepoSearchPaths {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(base, e.Name())
			if seen[p] || !git.IsRepo(p) {
				continue
			}
			seen[p] = true
			out = append(out, syncTarget{name: e.Name(), path: p})
		}
	}
	slices.SortFunc(out, func(a, b syncTarget) int {
		if a.name != b.name {
			if a.name < b.name {
				return -1
			}
			return 1
		}
		return 0
	})
	return out, nil
}

// syncRepo brings one checkout's default branch up to the remote and reports
// which category the repo ended up in.
func syncRepo(path string, opts *syncOptions) syncResult {
	if !git.RemoteExists(path, syncRemote) {
		return syncResult{kind: syncNoRemote}
	}
	if !opts.noFetch {
		if err := git.Fetch(path, syncRemote); err != nil {
			return syncResult{kind: syncFailed, detail: fmt.Sprintf("fetch failed: %v", oneLine(err.Error()))}
		}
	}

	def, err := git.DefaultBranch(path, syncRemote)
	if err != nil {
		return syncResult{kind: syncSkipped, detail: oneLine(err.Error())}
	}
	status, err := git.Describe(path)
	if err != nil {
		return syncResult{kind: syncFailed, detail: oneLine(err.Error())}
	}
	if !status.OnBranch() {
		return syncResult{kind: syncOtherBranch, detail: fmt.Sprintf("detached HEAD\twant %s", def)}
	}
	if status.Branch != def {
		return syncResult{kind: syncOtherBranch, detail: fmt.Sprintf("on %s\twant %s", status.Branch, def)}
	}

	target := syncRemote + "/" + def
	before := git.ShortSHA(path, "HEAD")
	behind := git.CountCommits(path, "HEAD.."+target)
	if behind == 0 && git.ShortSHA(path, target) == before {
		return syncResult{kind: syncSynced, detail: def + "\tup to date"}
	}

	// Let git arbitrate rather than pre-judging the working tree: it refuses a
	// fast-forward that would discard local work, and its message says why.
	if err := git.FastForward(path, target); err != nil {
		return syncResult{kind: syncFailed, detail: oneLine(err.Error())}
	}
	return syncResult{kind: syncSynced, detail: fmt.Sprintf("%s\t%s -> %s (%d)", def, before, git.ShortSHA(path, "HEAD"), behind)}
}

// oneLine collapses a multi-line git error so it cannot break the column layout.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
