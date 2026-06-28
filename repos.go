package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
)

// reposHandler lists every repository the current PAT can access, by shelling
// out to `gh api`. This is the discovery counterpart to the passthrough proxy:
// the proxy can serve any accessible repo, and this endpoint says which those
// are. It is best-effort — failures surface as 502 rather than crashing.
func reposHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cmd := exec.Command("gh", "api", "--paginate",
			"user/repos?per_page=100&affiliation=owner,collaborator,organization_member",
			"--jq", ".[].full_name")
		out, err := cmd.Output()
		if err != nil {
			log.Error("failed to list repos", "err", err)
			http.Error(w, "broken-mirror: failed to list repos via gh: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, string(out))
	}
}
