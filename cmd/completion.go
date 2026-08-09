// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/config"
	"github.com/draftcode/mwt/internal/workspace"
)

// completionConfig loads the config directly: cobra serves completion requests
// through a hidden command, so the root PersistentPreRunE never runs and the
// package-level cfg is still nil.
func completionConfig() (*config.Config, bool) {
	if cfg != nil {
		return cfg, true
	}
	c, err := config.Load()
	if err != nil {
		return nil, false
	}
	return c, true
}

// completeWorkspaces completes the name of an existing workspace.
func completeWorkspaces(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	c, ok := completionConfig()
	if !ok {
		return nil, cobra.ShellCompDirectiveError
	}
	all, err := workspace.List(c)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	out := make([]string, 0, len(all))
	for _, ws := range all {
		if strings.HasPrefix(ws.Name, toComplete) {
			out = append(out, ws.Name+"\t"+repoNames(ws))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeSourceRepos completes repo names that can be added to a workspace.
func completeSourceRepos(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	c, ok := completionConfig()
	if !ok {
		return nil, cobra.ShellCompDirectiveError
	}
	return offer(sourceRepoNames(c), args, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeReposToAdd completes repo names not already checked out in the target workspace.
func completeReposToAdd(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	c, ok := completionConfig()
	if !ok {
		return nil, cobra.ShellCompDirectiveError
	}
	taken := slices.Clone(args)
	if ws, err := workspace.Find(c, cmd.Flag("workspace").Value.String()); err == nil {
		for _, r := range ws.Repos {
			taken = append(taken, r.Name)
		}
	}
	return offer(sourceRepoNames(c), taken, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeWorkspaceRepos completes the repos checked out in the workspace named by args[0].
func completeWorkspaceRepos(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	c, ok := completionConfig()
	if !ok {
		return nil, cobra.ShellCompDirectiveError
	}
	var name string
	if len(args) > 0 {
		name = args[0]
	}
	ws, err := workspace.Find(c, name)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(ws.Repos))
	for _, r := range ws.Repos {
		if strings.HasPrefix(r.Name, toComplete) {
			out = append(out, r.Name+"\t"+r.Path)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// noCompletion suppresses the shell's default file completion for a value that
// cannot be enumerated, such as a new branch name.
func noCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func sourceRepoNames(c *config.Config) []string {
	var out []string
	for name, r := range c.Repos {
		if r.Path != "" && isCheckout(config.Expand(r.Path)) {
			out = append(out, name)
		}
	}
	for _, base := range c.RepoSearchPaths {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if isCheckout(filepath.Join(base, e.Name())) {
				out = append(out, e.Name())
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// isCheckout reports whether dir holds a git checkout, without shelling out to
// git: completion runs this over every entry of every search path.
func isCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// offer keeps the names matching toComplete that are not already on the command line.
func offer(names, taken []string, toComplete string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if strings.HasPrefix(n, toComplete) && !slices.Contains(taken, n) {
			out = append(out, n)
		}
	}
	return out
}
