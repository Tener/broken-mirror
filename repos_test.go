package main

import "testing"

func TestFormatRepoListAnnotatesReadOnly(t *testing.T) {
	names := []string{"Tener/accresys", "Tener/blamer", "other/repo", "", "  "}
	all := newRepoMatcher(nil) // read_allow empty => all readable
	writable := func(f string) bool { return f == "Tener/accresys" }

	got := formatRepoList(names, all, writable)
	want := "Tener/accresys\n" +
		"Tener/blamer (read only)\n" +
		"other/repo (read only)\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatRepoListRespectsReadAllow(t *testing.T) {
	names := []string{"Tener/accresys", "other/repo"}
	read := newRepoMatcher([]string{"Tener/*"})
	writable := func(string) bool { return false }

	got := formatRepoList(names, read, writable)
	want := "Tener/accresys (read only)\n"
	if got != want {
		t.Fatalf("got %q, want %q (other/repo should be filtered out)", got, want)
	}
}
