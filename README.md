# mwt

Multi-repo git worktrees for concurrent Claude Code sessions.

A **workspace** is a named directory under `~/worktrees/` holding one git worktree per
participating repo, all on the same branch. Each concurrent session gets its own
workspace, so two sessions never touch the same working tree.

```
~/worktrees/feat-search/
├── .mwt.json
├── api/                  # worktree of ~/src/api    on feat/search
├── schema/               # worktree of ~/src/schema on feat/search
└── worker/               # added later, same branch
```

## Install

```
go install github.com/draftcode/mwt@latest
```

## Use

```
mwt config init                       # write ~/.config/mwt/config.toml
mwt new feat/search api schema
mwt start feat/search                 # claude, cwd = workspace root, every repo --add-dir'd
mwt add worker                        # from inside the workspace
mwt list
mwt status                            # branch / dirty / ahead / behind per repo
mwt sync                              # fast-forward every canonical checkout's default branch
mwt rm feat/search --delete-branch
mwt prune                             # drop workspaces whose PRs all merged
```

Repos are named, not path'd: `api` is found by scanning `repo_search_paths`.
A path (`../foo`, `~/src/foo`) also works.

The branch defaults to the workspace name; `-b` overrides it. Each repo branches from its
own `origin/HEAD` (falling back to `origin/main`, `origin/master`) after a fetch; `--base`
or a per-repo `base` overrides. If the branch already exists in a repo, it is checked out
instead of created.

`mwt rm` refuses when any repo has uncommitted files or commits that are not on a remote,
unless `--force`.

`mwt sync` fetches each canonical checkout and fast-forwards its default branch, so the
repos that workspaces branch from do not drift behind. Pass repo names to narrow it, or
`--no-fetch` to work from the remote-tracking refs as they are. Workspace worktrees are
never touched, and a checkout sitting on some other branch is skipped. The fast-forward
itself is left to git, so a diverged branch or a conflicting local edit is reported and the
run exits non-zero instead of anything being overwritten.

The output is grouped by outcome — up to date / synced, on another branch, no origin
remote, skipped, failed — so a few dozen healthy repos collapse into one block and the
ones needing attention stand on their own. Empty groups are not printed.

The repos are fetched 8 at a time, which on a few dozen of them is the difference between
seconds and half a minute. A terminal gets a progress line while they run; redirected
output does not, and repos are ordered by name within each group either way.

`mwt prune` removes every workspace where each repo's branch has a MERGED pull request,
asked in one confirmation. A repo with no PR, an open or closed-unmerged PR, uncommitted
files, or unpushed commits keeps its whole workspace. It reads PR state with `gh`, so a
squash merge counts; the merged branch is deleted from each source repo unless
`--keep-branch`. `--dry-run` reports and stops.

`av` works normally inside a workspace: worktrees share the repo's `.git`, so stack state
is the same one the canonical checkout sees.

## Config

`~/.config/mwt/config.toml`:

```toml
worktree_root = "~/worktrees"
repo_search_paths = ["~/src", "~/alt_src"]
default_base = "origin/HEAD"
claude_command = ["claude"]

[defaults]
copy = [".env", ".env.local"]

[repos.api]
copy = ["config/master.key", ".env"]
setup = "bundle install --quiet"

[repos.web]
link = ["node_modules"]
setup = "pnpm install --frozen-lockfile"

[repos.some_repo]
path = "~/elsewhere/some_repo"   # when the repo is not under repo_search_paths
base = "origin/develop"
```

- `copy` — glob patterns copied from the canonical checkout into the fresh worktree.
- `link` — same, but symlinked. Good for heavy caches (`node_modules`, `.venv`) where
  sharing is acceptable; do not link anything a build writes to per-branch.
- `setup` — shell command run in the worktree after copy/link, with `MWT_WORKSPACE`,
  `MWT_WORKSPACE_ROOT`, `MWT_BRANCH`, `MWT_REPO`, `MWT_REPO_PATH`, `MWT_SOURCE_PATH` set.

Repos are hydrated in parallel; each one's output is printed prefixed with the repo name
once all finish.

Note: a `.gitignore` entry written as `node_modules/` does **not** ignore a `node_modules`
*symlink*. If a linked path shows up as dirty, change the pattern to `node_modules`.

## Completion

```zsh
mwt completion zsh > <a dir on your fpath>/_mwt   # then start a new shell
```

Workspace names, repo names, and `-w` complete dynamically by calling the binary, so
regenerating the script is only needed when commands or flags change. `mwt add` and
`mwt new` omit repos already on the command line, and `mwt add` also omits the ones
already checked out in the workspace. `bash`, `fish`, and `powershell` work the same way.

## Shell helper

`mwt path` prints a workspace or repo directory, for a `cd` wrapper:

```zsh
mw() { cd "$(mwt path "$@")" }        # mw feat/search [repo]
```

`mwt path --pr` takes a pull request URL or number instead, resolving it to the
worktree holding the branch it was opened from — useful when a review link is all you
have and the workspace name is not obvious:

```zsh
mwt path --pr https://github.com/acme/widget/pull/446
# → ~/worktrees/fix-flaky-login/widget

mwpr() { local p; p=$(mwt path --pr "$1") || return; cd "$p" }
```

The match is made against the `gh stack` state each worktree keeps, so any branch of
a stack resolves, not just the one currently checked out; when the pull request is
not recorded in any stack, `gh` is asked for its branch and the checkouts are
searched instead. A branch that is not the checkout is still printed, with a note on
stderr saying which `gh stack checkout` to run. It exits nonzero when no worktree
holds the branch, rather than falling back to the canonical checkout.

`mwt list --json` prints every workspace with its root, branch, and repo paths, for
scripting against. Bound to a key with `fzf`, it becomes a jump-to-worktree shortcut:

```zsh
mwt-cd-widget() {
  emulate -L zsh
  (( $+commands[mwt] )) || { zle -M "mwt: not installed"; return 1 }
  local -a lines
  lines=(${(f)"$(mwt list --json 2>/dev/null | jq -r '.[] | [.name, .root] | @tsv')"})
  (( $#lines )) || { zle -M "mwt: no workspaces"; return 1 }
  local selected
  selected=$(print -l -- $lines |
    fzf --prompt='mwt> ' --height=40% --reverse --with-nth=1 --delimiter='\t') || return 0
  [[ -n $selected ]] || return 0
  BUFFER="cd -- ${(q)${selected#*$'\t'}}"
  zle accept-line
}
zle -N mwt-cd-widget
bindkey '^G' mwt-cd-widget
```
