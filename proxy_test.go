package main

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBasicAuthHeader(t *testing.T) {
	got := basicAuthHeader("secret-token")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:secret-token"))
	if got != want {
		t.Fatalf("basicAuthHeader = %q, want %q", got, want)
	}
}

func TestIsWriteRequest(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"upload-pack advert", "/o/r/info/refs?service=git-upload-pack", false},
		{"upload-pack data", "/o/r/git-upload-pack", false},
		{"receive-pack advert", "/o/r/info/refs?service=git-receive-pack", true},
		{"receive-pack data", "/o/r/git-receive-pack", true},
		{"dumb file", "/o/r/HEAD", false},
		{"upload-archive", "/o/r/git-upload-archive", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if got := isWriteRequest(req); got != tc.want {
				t.Fatalf("isWriteRequest(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestRepoFullName(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"/owner/repo/info/refs", "owner/repo", true},
		{"/owner/repo.git/git-receive-pack", "owner/repo", true},
		{"/owner/repo", "owner/repo", true},
		{"/owner", "", false},
		{"/", "", false},
	}
	for _, tc := range cases {
		got, ok := repoFullName(tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("repoFullName(%q) = (%q,%v), want (%q,%v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

// newTestProxy wires a gitProxy to a stub upstream and returns the handler plus
// pointers recording whether the upstream was hit and the auth header it saw.
func newTestProxy(t *testing.T, readAllow, writeAllow []string) (http.Handler, *bool, *string) {
	t.Helper()
	return newTestProxyPolicy(t, readAllow, writeAllow, nil)
}

func newTestProxyPolicy(t *testing.T, readAllow, writeAllow []string, wp *WritePolicy) (http.Handler, *bool, *string) {
	t.Helper()
	var upstreamHit bool
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		gotAuth = r.Header.Get("Authorization")
		// Drain the body so streaming/rebuilt request bodies are exercised.
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "upstream-ok:"+r.URL.Path)
	}))
	t.Cleanup(upstream.Close)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	return newGitProxy(u, staticToken("tok123"), readAllow, writeAllow, wp, testLogger()), &upstreamHit, &gotAuth
}

func staticToken(t string) func() string { return func() string { return t } }

func TestProxyRejectsPushForUnlistedRepo(t *testing.T) {
	// owner/repo is NOT in the allowlist.
	proxy, hit, _ := newTestProxy(t, nil, []string{"someone/else"})

	for _, target := range []string{
		"/owner/repo/info/refs?service=git-receive-pack",
		"/owner/repo/git-receive-pack",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(""))
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", target, rec.Code)
		}
		if *hit {
			t.Errorf("%s: upstream was contacted on a rejected push", target)
		}
	}
}

func TestProxyForwardsUploadPackWithAuth(t *testing.T) {
	proxy, hit, gotAuth := newTestProxy(t, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/owner/repo/info/refs?service=git-upload-pack", nil)
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !*hit {
		t.Fatal("upstream was not contacted for an upload-pack request")
	}
	if want := basicAuthHeader("tok123"); *gotAuth != want {
		t.Fatalf("upstream Authorization = %q, want %q", *gotAuth, want)
	}
	if !strings.Contains(rec.Body.String(), "upstream-ok:/owner/repo/info/refs") {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestProxyAllowsPushForListedRepo(t *testing.T) {
	// owner/repo IS in the allowlist (case-insensitive match).
	proxy, hit, _ := newTestProxy(t, nil, []string{"Owner/Repo"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/owner/repo/git-receive-pack", strings.NewReader(""))
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (push allowed for listed repo)", rec.Code)
	}
	if !*hit {
		t.Fatal("upstream was not contacted for an allowed push")
	}
}

func TestProxyPushIsolation(t *testing.T) {
	// A listed repo must not grant push to a different, unlisted repo.
	proxy, hit, _ := newTestProxy(t, nil, []string{"owner/allowed"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/owner/other/git-receive-pack", strings.NewReader(""))
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for unlisted repo", rec.Code)
	}
	if *hit {
		t.Fatal("upstream contacted for an unlisted repo push")
	}
}

func TestProxyReadFilterDefaultsToAll(t *testing.T) {
	// No read patterns => everything readable.
	proxy, hit, _ := newTestProxy(t, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/any/repo/info/refs?service=git-upload-pack", nil)
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !*hit {
		t.Fatalf("status=%d hit=%v, want 200 and upstream contacted", rec.Code, *hit)
	}
}

func TestProxyReadFilterAllowsMatch(t *testing.T) {
	proxy, hit, _ := newTestProxy(t, []string{"Owner/*"}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/owner/anything/info/refs?service=git-upload-pack", nil)
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !*hit {
		t.Fatalf("status=%d hit=%v, want 200 (matches Owner/*)", rec.Code, *hit)
	}
}

func TestProxyReadFilterRejectsNonMatch(t *testing.T) {
	proxy, hit, _ := newTestProxy(t, []string{"owner/*"}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/other/repo/info/refs?service=git-upload-pack", nil)
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403 for repo outside read_allow", rec.Code)
	}
	if *hit {
		t.Fatal("upstream contacted for a repo outside read_allow")
	}
}

func TestProxyWritePolicyAllowsMatchingBranch(t *testing.T) {
	wp := &WritePolicy{Branches: []string{"main"}}
	proxy, hit, _ := newTestProxyPolicy(t, nil, []string{"owner/repo"}, wp)

	body := receivePackBody(cmd(zeroOID, oneOID, "refs/heads/main"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/owner/repo/git-receive-pack", strings.NewReader(body))
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !*hit {
		t.Fatalf("status=%d hit=%v, want 200 and forwarded", rec.Code, *hit)
	}
}

func TestProxyWritePolicyRejectsBranch(t *testing.T) {
	wp := &WritePolicy{Branches: []string{"main"}}
	proxy, hit, _ := newTestProxyPolicy(t, nil, []string{"owner/repo"}, wp)

	body := receivePackBody(cmd(zeroOID, oneOID, "refs/heads/forbidden"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/owner/repo/git-receive-pack", strings.NewReader(body))
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403 for branch outside write_policy", rec.Code)
	}
	if *hit {
		t.Fatal("upstream contacted for a policy-rejected push")
	}
}

func TestProxyWritePolicyAllowsAdvertisement(t *testing.T) {
	// The receive-pack ref advertisement carries no commands, so policy is not
	// applied; it must pass through for a writable repo.
	wp := &WritePolicy{Branches: []string{"main"}}
	proxy, hit, _ := newTestProxyPolicy(t, nil, []string{"owner/repo"}, wp)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/owner/repo/info/refs?service=git-receive-pack", nil)
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !*hit {
		t.Fatalf("status=%d hit=%v, want advertisement forwarded", rec.Code, *hit)
	}
}

func TestProxyWritePolicyForwardsFullBody(t *testing.T) {
	// The rebuilt request body must reach the upstream byte-for-byte identical.
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	wp := &WritePolicy{Branches: []string{"main"}}
	proxy := newGitProxy(u, staticToken("tok123"), nil, []string{"owner/repo"}, wp, testLogger())

	body := receivePackBody(cmd(zeroOID, oneOID, "refs/heads/main"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/owner/repo/git-receive-pack", strings.NewReader(body))
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	if string(got) != body {
		t.Fatalf("upstream body mismatch:\n got = %q\nwant = %q", got, body)
	}
}
