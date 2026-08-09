// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

// Package config loads mwt's user configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the top-level mwt configuration, read from ~/.config/mwt/config.toml.
type Config struct {
	WorktreeRoot    string         `toml:"worktree_root"`
	RepoSearchPaths []string       `toml:"repo_search_paths"`
	DefaultBase     string         `toml:"default_base"`
	ClaudeCommand   []string       `toml:"claude_command"`
	Defaults        RepoConfig     `toml:"defaults"`
	Repos           map[string]Rep `toml:"repos"`
}

// Rep is the per-repo override block under [repos.<name>].
type Rep struct {
	RepoConfig
	Path string `toml:"path"`
}

// RepoConfig describes how to hydrate a fresh worktree for a repo.
type RepoConfig struct {
	Copy  []string `toml:"copy"`
	Link  []string `toml:"link"`
	Setup string   `toml:"setup"`
	Base  string   `toml:"base"`
}

// Path returns the config file location, honoring XDG_CONFIG_HOME.
func Path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "mwt", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mwt", "config.toml")
}

// Load reads the config file, filling in defaults for anything unset.
func Load() (*Config, error) {
	cfg := &Config{}
	path := Path()
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	if cfg.WorktreeRoot == "" {
		cfg.WorktreeRoot = "~/worktrees"
	}
	if len(cfg.RepoSearchPaths) == 0 {
		cfg.RepoSearchPaths = []string{"~/src", "~/alt_src"}
	}
	if cfg.DefaultBase == "" {
		cfg.DefaultBase = "origin/HEAD"
	}
	if len(cfg.ClaudeCommand) == 0 {
		cfg.ClaudeCommand = []string{"claude"}
	}

	cfg.WorktreeRoot = Expand(cfg.WorktreeRoot)
	for i, p := range cfg.RepoSearchPaths {
		cfg.RepoSearchPaths[i] = Expand(p)
	}
	return cfg, nil
}

// ForRepo merges the defaults block with the repo-specific block.
func (c *Config) ForRepo(name string) RepoConfig {
	merged := RepoConfig{
		Copy:  append([]string{}, c.Defaults.Copy...),
		Link:  append([]string{}, c.Defaults.Link...),
		Setup: c.Defaults.Setup,
		Base:  c.Defaults.Base,
	}
	r, ok := c.Repos[name]
	if !ok {
		return merged
	}
	merged.Copy = append(merged.Copy, r.Copy...)
	merged.Link = append(merged.Link, r.Link...)
	if r.Setup != "" {
		merged.Setup = r.Setup
	}
	if r.Base != "" {
		merged.Base = r.Base
	}
	return merged
}

// Expand resolves a leading ~ and any environment variables in a path.
func Expand(p string) string {
	p = os.ExpandEnv(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
