// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/draftcode/mwt/internal/config"
)

const sampleConfig = `# mwt configuration

worktree_root = "~/worktrees"
repo_search_paths = ["~/src", "~/alt_src"]
default_base = "origin/HEAD"

# Applied to every repo before its own block.
[defaults]
copy = [".env", ".env.local"]

[repos.api]
copy = ["config/master.key", ".env"]
setup = "bundle install --quiet"

[repos.web]
link = ["node_modules"]
setup = "pnpm install --frozen-lockfile"
`

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show the config file path and contents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.Path()
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s does not exist; run `mwt config init` to create it\n", path)
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "# %s\n%s", path, data)
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Write a starter config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.Path()
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists", path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(sampleConfig), 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	})
	return cmd
}
