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
func newTestProxy(t *testing.T, writeAllow []string) (http.Handler, *bool, *string) {
	t.Helper()
	var upstreamHit bool
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "upstream-ok:"+r.URL.Path)
	}))
	t.Cleanup(upstream.Close)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	return newGitProxy(u, "tok123", writeAllow, testLogger()), &upstreamHit, &gotAuth
}

func TestProxyRejectsPushForUnlistedRepo(t *testing.T) {
	// owner/repo is NOT in the allowlist.
	proxy, hit, _ := newTestProxy(t, []string{"someone/else"})

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
	proxy, hit, gotAuth := newTestProxy(t, nil)

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
	proxy, hit, _ := newTestProxy(t, []string{"Owner/Repo"})

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
	proxy, hit, _ := newTestProxy(t, []string{"owner/allowed"})

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
