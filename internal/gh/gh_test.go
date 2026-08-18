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

func TestRefMatches(t *testing.T) {
	url := "https://github.com/acme/widget/pull/7"
	for _, tc := range []struct {
		ref  Ref
		want bool
	}{
		{Ref{Repo: "acme/widget", Number: 7}, true},
		{Ref{Repo: "ACME/Widget", Number: 7}, true},
		{Ref{Number: 7}, true},
		{Ref{Repo: "acme/gadget", Number: 7}, false},
		{Ref{Repo: "acme/widget", Number: 8}, false},
	} {
		if got := tc.ref.Matches(url); got != tc.want {
			t.Errorf("%+v.Matches(%s) = %v, want %v", tc.ref, url, got, tc.want)
		}
	}
}

func TestRefMatchesRejectsNonPRURL(t *testing.T) {
	if (Ref{Number: 7}).Matches("https://github.com/acme/widget/issues/7") {
		t.Error("an issue URL matched a pull request reference")
	}
}
