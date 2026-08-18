// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"testing"

	"github.com/draftcode/mwt/internal/workspace"
)

// The base of a stack is recorded in every workspace stacked on top of it, so the
// worktree that has it checked out is the one that owns it.
func TestPreferCheckedOutPicksTheOwningWorktree(t *testing.T) {
	own := workspace.Match{Workspace: "feat/base", Path: "/w/base", Branch: "feat/base", CheckedOut: "feat/base"}
	above := workspace.Match{Workspace: "feat/top", Path: "/w/top", Branch: "feat/base", CheckedOut: "feat/top"}

	got := preferCheckedOut([]workspace.Match{above, own})
	if len(got) != 1 || got[0].Path != own.Path {
		t.Errorf("got %+v, want only %s", got, own.Path)
	}
}

// With nothing checked out on the branch there is no owner to pick, and with
// several there is no single one; both stay ambiguous rather than guess.
func TestPreferCheckedOutKeepsUnresolvableSets(t *testing.T) {
	none := []workspace.Match{
		{Path: "/w/a", Branch: "feat/base", CheckedOut: "feat/a"},
		{Path: "/w/b", Branch: "feat/base", CheckedOut: "feat/b"},
	}
	if got := preferCheckedOut(none); len(got) != 2 {
		t.Errorf("got %+v, want both kept", got)
	}

	both := []workspace.Match{
		{Path: "/w/a", Branch: "feat/base", CheckedOut: "feat/base"},
		{Path: "/w/b", Branch: "feat/base", CheckedOut: "feat/base"},
	}
	if got := preferCheckedOut(both); len(got) != 2 {
		t.Errorf("got %+v, want both kept", got)
	}
}
