// Copyright 2026 Masaya Suzuki
// SPDX-License-Identifier: Apache-2.0

package gh

import "testing"

func TestParseRef(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantRepo string
		wantNum  int
	}{
		{"https://github.com/ivry-inc/crossbar/pull/446", "ivry-inc/crossbar", 446},
		{"https://github.com/ivry-inc/crossbar/pull/446/files", "ivry-inc/crossbar", 446},
		{"https://github.com/ivry-inc/crossbar/pull/446#discussion_r1", "ivry-inc/crossbar", 446},
		{"github.com/ivry-inc/crossbar/pull/446", "ivry-inc/crossbar", 446},
		{"ivry-inc/crossbar#446", "ivry-inc/crossbar", 446},
		{"446", "", 446},
		{"#446", "", 446},
		{"  https://github.com/ivry-inc/crossbar/pull/446  ", "ivry-inc/crossbar", 446},
	} {
		got, err := ParseRef(tc.in)
		if err != nil {
			t.Errorf("ParseRef(%q) failed: %v", tc.in, err)
			continue
		}
		if got.Repo != tc.wantRepo || got.Number != tc.wantNum {
			t.Errorf("ParseRef(%q) = {%q, %d}, want {%q, %d}", tc.in, got.Repo, got.Number, tc.wantRepo, tc.wantNum)
		}
	}
}

func TestParseRefRejectsNonPullRequest(t *testing.T) {
	for _, in := range []string{
		"",
		"https://github.com/ivry-inc/crossbar/issues/446",
		"https://github.com/ivry-inc/crossbar",
		"feat/thing",
		"0",
		"-1",
	} {
		if got, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q) = %+v, want an error", in, got)
		}
	}
}
