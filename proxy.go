package main

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// gitProxy reverse-proxies git smart-HTTP requests to an upstream git host
// (github.com by default), injecting HTTP Basic auth derived from a PAT. Reads
// are always allowed; a push (git-receive-pack) is allowed only for repos in the
// explicit writeAllow set — everything else is read-only.
type gitProxy struct {
	upstream   *url.URL
	authHeader string
	writeAllow map[string]bool // lowercased "owner/repo" -> allowed
	log        *slog.Logger
	rp         *httputil.ReverseProxy
}

// basicAuthHeader builds the Authorization header value GitHub's git smart-HTTP
// endpoints accept for a PAT. GitHub rejects "Bearer <token>" (401) but accepts
// Basic auth with the username "x-access-token" and the token as the password.
func basicAuthHeader(token string) string {
	creds := "x-access-token:" + token
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))
}

// newWriteAllowSet normalizes a list of "OWNER/REPO" names into a lookup set
// keyed by lowercase full name (GitHub treats owner/repo case-insensitively).
func newWriteAllowSet(list []string) map[string]bool {
	set := make(map[string]bool, len(list))
	for _, e := range list {
		set[strings.ToLower(e)] = true
	}
	return set
}

func newGitProxy(upstream *url.URL, token string, writeAllow []string, log *slog.Logger) *gitProxy {
	p := &gitProxy{
		upstream:   upstream,
		authHeader: basicAuthHeader(token),
		writeAllow: newWriteAllowSet(writeAllow),
		log:        log,
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			// Route the outbound request at the upstream host and present that
			// host in the Host header so virtual-hosted backends (github.com)
			// resolve the repo correctly.
			r.Out.Host = upstream.Host
			// We supply our own credentials; never forward a client's.
			r.Out.Header.Set("Authorization", p.authHeader)
		},
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
	return p
}

// isWriteRequest reports whether r is a git push (receive-pack) request, in
// either the ref-advertisement (GET /info/refs?service=git-receive-pack) or the
// data (POST /git-receive-pack) phase.
func isWriteRequest(r *http.Request) bool {
	if r.URL.Query().Get("service") == "git-receive-pack" {
		return true
	}
	return strings.HasSuffix(r.URL.Path, "/git-receive-pack")
}

// repoFullName extracts the "owner/repo" identity from a smart-HTTP request
// path such as "/owner/repo/info/refs" or "/owner/repo.git/git-receive-pack".
func repoFullName(path string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 {
		return "", false
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" {
		return "", false
	}
	return owner + "/" + repo, true
}

// writeAllowed reports whether the repo targeted by r is in the explicit
// write allowlist.
func (p *gitProxy) writeAllowed(r *http.Request) bool {
	full, ok := repoFullName(r.URL.Path)
	if !ok {
		return false
	}
	return p.writeAllow[strings.ToLower(full)]
}

func (p *gitProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isWriteRequest(r) && !p.writeAllowed(r) {
		full, _ := repoFullName(r.URL.Path)
		p.log.Warn("rejected write request", "method", r.Method, "path", r.URL.Path, "repo", full)
		http.Error(w, "broken-mirror: read-only proxy — this repo is not in write_allow", http.StatusForbidden)
		return
	}
	p.rp.ServeHTTP(w, r)
}
