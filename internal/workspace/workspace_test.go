// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package workspace

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/draftcode/mwt/internal/config"
	"github.com/draftcode/mwt/internal/gh"
	"github.com/draftcode/mwt/internal/ghstack"
	"github.com/draftcode/mwt/internal/git"
)

// srcRepo builds a repo with one commit that worktrees can be cut from.
func srcRepo(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, "src", name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@test.invalid"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "-m", "base"},
	} {
		if _, err := git.Run(path, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return path
}

func testConfig(root string, repos map[string]config.Rep) *config.Config {
	return &config.Config{
		WorktreeRoot:    filepath.Join(root, "worktrees"),
		RepoSearchPaths: []string{filepath.Join(root, "src")},
		DefaultBase:     "main",
		Repos:           repos,
	}
}

func newWorkspace(t *testing.T, cfg *config.Config, name string) *Workspace {
	t.Helper()
	wsRoot := filepath.Join(cfg.WorktreeRoot, DirName(name))
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Name: name, Branch: name, Root: wsRoot, Created: time.Now()}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}
	return ws
}

// A setup command that fails must not cost the workspace its bookkeeping: the
// worktree exists by then, and an unrecorded worktree is an unprotected one.
func TestAddReposRecordsWorktreeWhenSetupFails(t *testing.T) {
	root := t.TempDir()
	srcRepo(t, root, "widget")
	cfg := testConfig(root, map[string]config.Rep{
		"widget": {RepoConfig: config.RepoConfig{Setup: "exit 3"}},
	})
	ws := newWorkspace(t, cfg, "feat/thing")

	err := AddRepos(cfg, ws, []string{"widget"}, AddOptions{Out: io.Discard})
	if err == nil {
		t.Fatal("AddRepos succeeded despite a failing setup command")
	}

	reloaded, err := Load(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Repo("widget"); !ok {
		t.Errorf("widget missing from %s after failed setup: %+v", MetaFile, reloaded.Repos)
	}
}

// A worktree left behind by an interrupted run is invisible to every command
// until it is adopted, and invisible means unprotected.
func TestLoadAdoptsUnrecordedWorktree(t *testing.T) {
	root := t.TempDir()
	source := srcRepo(t, root, "widget")
	cfg := testConfig(root, nil)
	ws := newWorkspace(t, cfg, "feat/thing")

	path := filepath.Join(ws.Root, "widget")
	if err := git.AddWorktree(source, path, "feat/thing", "main"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := reloaded.Repo("widget")
	if !ok {
		t.Fatalf("unrecorded worktree not adopted: %+v", reloaded.Repos)
	}
	if r.Path != path {
		t.Errorf("path = %q, want %q", r.Path, path)
	}
	if r.Source != source {
		t.Errorf("source = %q, want %q", r.Source, source)
	}
}

// A recorded repo whose directory is gone must stay in the list, so commands can
// report it instead of pretending the workspace is empty.
func TestLoadKeepsRecordedRepoWithMissingDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root, nil)
	ws := newWorkspace(t, cfg, "feat/thing")
	ws.Repos = []Repo{{
		Name:   "widget",
		Source: filepath.Join(root, "src", "widget"),
		Path:   filepath.Join(ws.Root, "widget"),
	}}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repos) != 1 {
		t.Errorf("got %d repos, want the missing one kept: %+v", len(reloaded.Repos), reloaded.Repos)
	}
}

func TestFindByBranchPicksTheMatchingWorktree(t *testing.T) {
	root := t.TempDir()
	source := srcRepo(t, root, "widget")
	cfg := testConfig(root, nil)
	for _, name := range []string{"feat/one", "feat/two"} {
		ws := newWorkspace(t, cfg, name)
		if err := git.AddWorktree(source, filepath.Join(ws.Root, "widget"), name, "main"); err != nil {
			t.Fatal(err)
		}
	}

	matches, err := FindByBranch(cfg, "feat/two", "widget")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1: %+v", len(matches), matches)
	}
	if want := filepath.Join(cfg.WorktreeRoot, "feat-two", "widget"); matches[0].Path != want {
		t.Errorf("path = %q, want %q", matches[0].Path, want)
	}
}

// Stacking a branch inside a workspace leaves the worktree on a branch the
// metadata never mentions, and that branch is the one a pull request names.
func TestFindByBranchFollowsTheCheckoutNotTheMetadata(t *testing.T) {
	root := t.TempDir()
	source := srcRepo(t, root, "widget")
	cfg := testConfig(root, nil)
	ws := newWorkspace(t, cfg, "feat/base")
	path := filepath.Join(ws.Root, "widget")
	if err := git.AddWorktree(source, path, "feat/base", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Run(path, "checkout", "-b", "feat/stacked"); err != nil {
		t.Fatal(err)
	}

	matches, err := FindByBranch(cfg, "feat/stacked", "widget")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want the stacked branch found: %+v", len(matches), matches)
	}
	if matches[0].Path != path {
		t.Errorf("path = %q, want %q", matches[0].Path, path)
	}

	stale, err := FindByBranch(cfg, "feat/base", "widget")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("recorded branch still matched after checkout moved: %+v", stale)
	}
}

func TestFindByBranchNarrowsToRepo(t *testing.T) {
	root := t.TempDir()
	widget := srcRepo(t, root, "widget")
	gadget := srcRepo(t, root, "gadget")
	cfg := testConfig(root, nil)
	ws := newWorkspace(t, cfg, "feat/both")
	for _, s := range []string{widget, gadget} {
		if err := git.AddWorktree(s, filepath.Join(ws.Root, filepath.Base(s)), "feat/both", "main"); err != nil {
			t.Fatal(err)
		}
	}

	all, err := FindByBranch(cfg, "feat/both", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d matches, want both repos: %+v", len(all), all)
	}

	one, err := FindByBranch(cfg, "feat/both", "gadget")
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Repo != "gadget" {
		t.Errorf("repo filter returned %+v", one)
	}
}

// A plain directory is not a worktree and must not be adopted as one.
func TestLoadIgnoresNonRepoDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root, nil)
	ws := newWorkspace(t, cfg, "feat/thing")
	if err := os.MkdirAll(filepath.Join(ws.Root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repos) != 0 {
		t.Errorf("adopted a non-repo directory: %+v", reloaded.Repos)
	}
}

// writeStack records a gh stack state for a worktree. Each entry is a branch name
// and the pull request URL opened from it, in stack order.
func writeStack(t *testing.T, worktree string, prs map[string]string, order []string) {
	t.Helper()
	gitDir, err := git.Dir(worktree)
	if err != nil {
		t.Fatal(err)
	}
	branches := make([]any, 0, len(order))
	for _, name := range order {
		b := map[string]any{"branch": name}
		if url := prs[name]; url != "" {
			b["pullRequest"] = map[string]any{"url": url}
		}
		branches = append(branches, b)
	}
	state := map[string]any{
		"schemaVersion": 1,
		"stacks":        []any{map[string]any{"branches": branches}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, ghstack.StateFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// stacked builds a workspace whose single worktree carries a two-branch stack and
// has the lower branch checked out, then returns the worktree path.
func stacked(t *testing.T, cfg *config.Config, name, repo, lower, upper, lowerPR, upperPR string) string {
	t.Helper()
	source := srcRepo(t, filepath.Dir(cfg.RepoSearchPaths[0]), repo)
	ws := newWorkspace(t, cfg, name)
	path := filepath.Join(ws.Root, repo)
	if err := git.AddWorktree(source, path, lower, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Run(path, "branch", upper); err != nil {
		t.Fatal(err)
	}
	writeStack(t, path, map[string]string{lower: lowerPR, upper: upperPR}, []string{lower, upper})
	return path
}

// The whole point of the lookup: a review lands on a branch further up the stack,
// which is not the one the worktree currently has checked out.
func TestFindByPRLocatesABranchThatIsNotCheckedOut(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root, nil)
	path := stacked(t, cfg, "feat/base", "widget", "feat/base", "feat/top",
		"https://github.com/acme/widget/pull/1", "https://github.com/acme/widget/pull/2")

	ref, err := gh.ParseRef("https://github.com/acme/widget/pull/2")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := FindByPR(cfg, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1: %+v", len(matches), matches)
	}
	m := matches[0]
	if m.Path != path {
		t.Errorf("path = %q, want %q", m.Path, path)
	}
	if m.Branch != "feat/top" {
		t.Errorf("branch = %q, want the pull request's branch feat/top", m.Branch)
	}
	if m.CheckedOut != "feat/base" {
		t.Errorf("checked out = %q, want feat/base", m.CheckedOut)
	}
}

// Pull request numbers repeat across repos, so a reference carrying a repo must
// not match a same-numbered pull request somewhere else.
func TestFindByPRRejectsTheSameNumberInAnotherRepo(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root, nil)
	stacked(t, cfg, "feat/base", "widget", "feat/base", "feat/top",
		"https://github.com/acme/widget/pull/1", "https://github.com/acme/widget/pull/2")

	ref, err := gh.ParseRef("https://github.com/acme/gadget/pull/2")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := FindByPR(cfg, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("matched another repo's pull request: %+v", matches)
	}
}

// A branch resolved through gh is looked up the same way, so a stacked branch that
// is not checked out still finds its worktree.
func TestFindByBranchFindsAStackBranchNotCheckedOut(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root, nil)
	path := stacked(t, cfg, "feat/base", "widget", "feat/base", "feat/top", "", "")

	matches, err := FindByBranch(cfg, "feat/top", "widget")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Path != path {
		t.Fatalf("got %+v, want the worktree at %s", matches, path)
	}
}
