// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/draftcode/mwt/internal/config"
	"github.com/draftcode/mwt/internal/git"
)

// AddOptions controls how repos are attached to a workspace.
type AddOptions struct {
	Base    string
	Fetch   bool
	NoSetup bool
	Out     io.Writer
}

// AddRepos creates a worktree per repo in parallel and hydrates each one.
func AddRepos(cfg *config.Config, ws *Workspace, repos []string, opts AddOptions) error {
	if opts.Out == nil {
		opts.Out = os.Stderr
	}

	type resolved struct {
		name, source string
	}
	var targets []resolved
	for _, spec := range repos {
		name, source, err := ResolveSource(cfg, spec)
		if err != nil {
			return err
		}
		if _, ok := ws.Repo(name); ok {
			fmt.Fprintf(opts.Out, "%s: already in workspace, skipping\n", name)
			continue
		}
		targets = append(targets, resolved{name, source})
	}
	if len(targets) == 0 {
		return nil
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		errs   []error
		outBuf = map[string]*strings.Builder{}
	)
	// record persists a repo the moment its worktree exists, before the slow
	// hydration steps. Recording only at the end loses the whole workspace's
	// bookkeeping to one failed setup command or one interrupt, leaving worktrees
	// on disk that mwt cannot see and will not protect.
	record := func(r Repo) error {
		mu.Lock()
		defer mu.Unlock()
		ws.Repos = append(ws.Repos, r)
		return ws.Save()
	}
	for _, t := range targets {
		outBuf[t.name] = &strings.Builder{}
		wg.Add(1)
		go func(name, source string) {
			defer wg.Done()
			log := outBuf[name]
			path := filepath.Join(ws.Root, name)
			if err := addOne(cfg, ws, name, source, path, opts, record, log); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
				mu.Unlock()
			}
		}(t.name, t.source)
	}
	wg.Wait()

	for _, t := range targets {
		if s := outBuf[t.name].String(); s != "" {
			for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
				fmt.Fprintf(opts.Out, "%s | %s\n", t.name, line)
			}
		}
	}
	return errors.Join(errs...)
}

func addOne(cfg *config.Config, ws *Workspace, name, source, path string, opts AddOptions, record func(Repo) error, log *strings.Builder) error {
	rc := cfg.ForRepo(name)

	if opts.Fetch {
		if err := git.RunPassthrough(source, "fetch", "--quiet", "origin"); err != nil {
			fmt.Fprintf(log, "fetch failed, continuing with local refs: %v\n", err)
		}
	}

	base := opts.Base
	if base == "" {
		base = rc.Base
	}
	if base == "" {
		base = cfg.DefaultBase
	}
	base, err := git.DefaultBase(source, base)
	if err != nil {
		return err
	}

	if err := git.AddWorktree(source, path, ws.Branch, base); err != nil {
		return err
	}
	fmt.Fprintf(log, "worktree %s (%s from %s)\n", path, ws.Branch, base)
	if err := record(Repo{Name: name, Source: source, Path: path}); err != nil {
		return err
	}

	for _, pattern := range rc.Copy {
		if err := transfer(source, path, pattern, false, log); err != nil {
			return err
		}
	}
	for _, pattern := range rc.Link {
		if err := transfer(source, path, pattern, true, log); err != nil {
			return err
		}
	}

	if rc.Setup != "" && !opts.NoSetup {
		fmt.Fprintf(log, "setup: %s\n", rc.Setup)
		cmd := exec.Command("sh", "-c", rc.Setup)
		cmd.Dir = path
		cmd.Env = append(os.Environ(),
			"MWT_WORKSPACE="+ws.Name,
			"MWT_WORKSPACE_ROOT="+ws.Root,
			"MWT_BRANCH="+ws.Branch,
			"MWT_REPO="+name,
			"MWT_REPO_PATH="+path,
			"MWT_SOURCE_PATH="+source,
		)
		out, err := cmd.CombinedOutput()
		log.Write(out)
		if err != nil {
			return fmt.Errorf("setup command failed: %w", err)
		}
	}
	return nil
}

func transfer(source, dest, pattern string, symlink bool, log *strings.Builder) error {
	matches, err := filepath.Glob(filepath.Join(source, pattern))
	if err != nil {
		return fmt.Errorf("bad pattern %q: %w", pattern, err)
	}
	if len(matches) == 0 {
		return nil
	}
	for _, src := range matches {
		rel, err := filepath.Rel(source, src)
		if err != nil {
			return err
		}
		dst := filepath.Join(dest, rel)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if symlink {
			if err := os.Symlink(src, dst); err != nil {
				return err
			}
			fmt.Fprintf(log, "link %s\n", rel)
			continue
		}
		if err := copyPath(src, dst); err != nil {
			return err
		}
		fmt.Fprintf(log, "copy %s\n", rel)
	}
	return nil
}

func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}
