package main

import "testing"

func TestRepoMatcher(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		full     string
		want     bool
	}{
		{"empty matches all", nil, "any/repo", true},
		{"star matches all", []string{"*"}, "any/repo", true},
		{"owner glob match", []string{"Tener/*"}, "Tener/accresys", true},
		{"owner glob non-match", []string{"Tener/*"}, "Other/accresys", false},
		{"case insensitive", []string{"tener/*"}, "Tener/Accresys", true},
		{"repo across owners", []string{"*/infra"}, "anyorg/infra", true},
		{"repo across owners non-match", []string{"*/infra"}, "anyorg/app", false},
		{"single char ?", []string{"o/rep?"}, "o/repo", true},
		{"single char ? non-match", []string{"o/rep?"}, "o/report", false},
		{"exact full name", []string{"o/r"}, "o/r", true},
		{"any of several", []string{"a/*", "b/*"}, "b/thing", true},
		{"none of several", []string{"a/*", "b/*"}, "c/thing", false},
		{"prefix glob", []string{"o/proj-*"}, "o/proj-x", true},
		{"dot is literal", []string{"o/a.b"}, "o/axb", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newRepoMatcher(tc.patterns)
			if got := m.match(tc.full); got != tc.want {
				t.Fatalf("match(%q) with %v = %v, want %v", tc.full, tc.patterns, got, tc.want)
			}
		})
	}
}
