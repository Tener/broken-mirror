package main

import (
	"regexp"
	"strings"
)

// repoMatcher tests "owner/repo" names against a set of glob patterns. It is
// used for the read allowlist, where wildcards are permitted. An empty pattern
// set matches everything (the default "all repos readable").
type repoMatcher struct {
	all bool
	res []*regexp.Regexp
}

func newRepoMatcher(patterns []string) *repoMatcher {
	m := &repoMatcher{}
	if len(patterns) == 0 {
		m.all = true
		return m
	}
	for _, p := range patterns {
		if p == "*" {
			m.all = true
		}
		m.res = append(m.res, globToRegexp(p))
	}
	return m
}

func (m *repoMatcher) match(fullName string) bool {
	if m.all {
		return true
	}
	for _, re := range m.res {
		if re.MatchString(fullName) {
			return true
		}
	}
	return false
}

// globToRegexp compiles a glob pattern into a case-insensitive anchored regexp.
// "*" matches any run of characters (including "/"), "?" matches a single
// character, and everything else is matched literally. Because all non-glob
// runes are quoted, the result is always a valid expression.
func globToRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
