package main

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
)

// reposHandler lists the repositories the current PAT can access, by shelling
// out to `gh api`. The list is filtered to what the proxy will actually serve
// (read_allow) and each entry is annotated "(read only)" unless the repo is in
// write_allow. It is best-effort — failures surface as 502.
func reposHandler(p *gitProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cmd := exec.Command("gh", "api", "--paginate",
			"user/repos?per_page=100&affiliation=owner,collaborator,organization_member",
			"--jq", ".[].full_name")
		out, err := cmd.Output()
		if err != nil {
			p.log.Error("failed to list repos", "err", err)
			http.Error(w, "broken-mirror: failed to list repos via gh: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, formatRepoList(strings.Split(string(out), "\n"), p.readAllow, p.writeAllowed))
	}
}

// formatRepoList filters full names by the read matcher and annotates each
// readable repo, marking those that are not writable as "(read only)".
func formatRepoList(fullNames []string, readAllow *repoMatcher, writable func(string) bool) string {
	var b strings.Builder
	for _, full := range fullNames {
		full = strings.TrimSpace(full)
		if full == "" || !readAllow.match(full) {
			continue
		}
		if writable(full) {
			fmt.Fprintf(&b, "%s\n", full)
		} else {
			fmt.Fprintf(&b, "%s (read only)\n", full)
		}
	}
	return b.String()
}
