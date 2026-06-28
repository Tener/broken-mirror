package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// gitProxy reverse-proxies git smart-HTTP requests to an upstream git host
// (github.com by default), injecting HTTP Basic auth derived from a PAT. A read
// (git-upload-pack) is allowed when the repo matches the readAllow patterns
// (default: all). A push (git-receive-pack) is allowed only for repos in the
// explicit writeAllow set — everything else is read-only.
type gitProxy struct {
	upstream   *url.URL
	token      func() string // resolved per request so rotation is picked up
	readAllow  *repoMatcher
	writeAllow map[string]bool // lowercased "owner/repo" -> allowed
	refPolicy  *refPolicy      // nil => no ref-level restriction
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

func newGitProxy(upstream *url.URL, token func() string, readAllow, writeAllow []string, writePolicy *WritePolicy, log *slog.Logger) *gitProxy {
	p := &gitProxy{
		upstream:   upstream,
		token:      token,
		readAllow:  newRepoMatcher(readAllow),
		writeAllow: newWriteAllowSet(writeAllow),
		refPolicy:  newRefPolicy(writePolicy),
		log:        log,
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			// Route the outbound request at the upstream host and present that
			// host in the Host header so virtual-hosted backends (github.com)
			// resolve the repo correctly.
			r.Out.Host = upstream.Host
			// We supply our own credentials (resolved fresh, to pick up token
			// rotation); never forward a client's.
			r.Out.Header.Set("Authorization", basicAuthHeader(p.token()))
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

// writeAllowed reports whether fullName is in the explicit write allowlist.
func (p *gitProxy) writeAllowed(fullName string) bool {
	return p.writeAllow[strings.ToLower(fullName)]
}

func (p *gitProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	full, ok := repoFullName(r.URL.Path)
	if !ok {
		http.Error(w, "broken-mirror: could not determine repository from request path", http.StatusBadRequest)
		return
	}

	if isWriteRequest(r) {
		if !p.writeAllowed(full) {
			p.log.Warn("rejected write request", "method", r.Method, "path", r.URL.Path, "repo", full)
			http.Error(w, "broken-mirror: read-only proxy — this repo is not in write_allow", http.StatusForbidden)
			return
		}
		if p.refPolicy != nil && isReceivePackData(r) {
			if reason, ok := p.enforceRefPolicy(r); !ok {
				p.log.Warn("rejected push by write_policy", "path", r.URL.Path, "repo", full, "reason", reason)
				http.Error(w, "broken-mirror: "+reason, http.StatusForbidden)
				return
			}
		}
	} else if !p.readAllow.match(full) {
		p.log.Warn("rejected read request", "method", r.Method, "path", r.URL.Path, "repo", full)
		http.Error(w, "broken-mirror: this repo is not in read_allow", http.StatusForbidden)
		return
	}

	p.rp.ServeHTTP(w, r)
}

// isReceivePackData reports whether r carries the actual push data (the
// ref-update commands and packfile), as opposed to the ref advertisement.
func isReceivePackData(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git-receive-pack")
}

// enforceRefPolicy parses the receive-pack request, checks every updated ref
// against the policy, and rebuilds r.Body so the upstream still receives the
// full original request. It fails closed: any parse error denies the push.
func (p *gitProxy) enforceRefPolicy(r *http.Request) (reason string, ok bool) {
	cmds, err := p.readReceivePackCommands(r)
	if err != nil {
		p.log.Warn("could not parse receive-pack request", "err", err, "path", r.URL.Path)
		return "could not parse push request for write_policy enforcement", false
	}
	for _, c := range cmds {
		if !p.refPolicy.allow(c.ref) {
			return fmt.Sprintf("push to %q is not permitted by write_policy", c.ref), false
		}
	}
	return "", true
}

// rewindBody re-presents an already-partially-read request body by replaying a
// consumed prefix ahead of the remaining stream, preserving the original Close.
type rewindBody struct {
	io.Reader
	closer io.Closer
}

func (b rewindBody) Close() error { return b.closer.Close() }

// readReceivePackCommands extracts the ref-update commands from r and leaves
// r.Body able to deliver the complete original request to the upstream. The
// uncompressed case streams (only the small command section is buffered); a
// gzip-encoded body is buffered whole, which is rare for receive-pack.
func (p *gitProxy) readReceivePackCommands(r *http.Request) ([]refUpdate, error) {
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		raw, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(raw)) // forward original compressed bytes
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		cmds, _, err := parseReceivePackCommands(zr)
		return cmds, err
	}

	orig := r.Body
	cmds, consumed, err := parseReceivePackCommands(orig)
	r.Body = rewindBody{io.MultiReader(bytes.NewReader(consumed), orig), orig}
	return cmds, err
}
