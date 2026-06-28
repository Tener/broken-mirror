package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// tokenRefreshInterval is how long a resolved token is reused before being
// re-resolved, so that gh-side rotation is picked up without a restart.
const tokenRefreshInterval = time.Minute

// resolveToken finds the upstream GitHub PAT, in priority order:
//  1. the --token flag (override),
//  2. the GH_TOKEN or GITHUB_TOKEN environment variables,
//  3. `gh auth token` (the gh CLI's stored credential).
func resolveToken(override string) (string, error) {
	if t := strings.TrimSpace(override); t != "" {
		return t, nil
	}
	for _, env := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			return t, nil
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("no token from --token/GH_TOKEN/GITHUB_TOKEN and `gh auth token` failed: %w", err)
	}
	t := strings.TrimSpace(string(out))
	if t == "" {
		return "", fmt.Errorf("`gh auth token` returned an empty token")
	}
	return t, nil
}

// tokenProvider caches a resolved token and re-resolves it after ttl elapses,
// so a rotated gh credential is picked up automatically. It is safe for
// concurrent use. A refresh failure keeps the previous token (logged), so a
// transient `gh` hiccup does not break in-flight clones.
type tokenProvider struct {
	override string
	ttl      time.Duration
	resolve  func(string) (string, error)
	now      func() time.Time
	log      *slog.Logger

	mu        sync.Mutex
	token     string
	fetchedAt time.Time
}

func newTokenProvider(override string, ttl time.Duration, log *slog.Logger) (*tokenProvider, error) {
	return newTokenProviderWith(override, ttl, resolveToken, time.Now, log)
}

// newTokenProviderWith allows injecting the resolver and clock for testing.
func newTokenProviderWith(override string, ttl time.Duration, resolve func(string) (string, error), now func() time.Time, log *slog.Logger) (*tokenProvider, error) {
	tp := &tokenProvider{override: override, ttl: ttl, resolve: resolve, now: now, log: log}
	tok, err := resolve(override)
	if err != nil {
		return nil, err
	}
	tp.token = tok
	tp.fetchedAt = now()
	return tp, nil
}

// get returns the current token, re-resolving it if the cache has gone stale.
func (tp *tokenProvider) get() string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.now().Sub(tp.fetchedAt) >= tp.ttl {
		tp.fetchedAt = tp.now() // advance even on failure to avoid hammering gh
		switch tok, err := tp.resolve(tp.override); {
		case err != nil:
			if tp.log != nil {
				tp.log.Warn("token refresh failed; keeping previous token", "err", err)
			}
		case tok != "":
			tp.token = tok
		}
	}
	return tp.token
}
