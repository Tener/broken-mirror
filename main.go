package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const version = "0.1.0"

func main() {
	cfgPath := flag.String("config", "", "path to TOML config file (default: ./broken-mirror.toml if present)")
	addr := flag.String("addr", "", "override listen address from config")
	upstream := flag.String("upstream", "", "override upstream git host from config")
	tokenFlag := flag.String("token", "", "override PAT from config (else GH_TOKEN/GITHUB_TOKEN/`gh auth token`)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("broken-mirror", version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Error("config error", "err", err)
		os.Exit(1)
	}
	// Explicitly-set flags override the config file.
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr":
			cfg.Addr = *addr
		case "upstream":
			cfg.Upstream = *upstream
		case "token":
			cfg.Token = *tokenFlag
		}
	})

	upstreamURL, err := url.Parse(cfg.Upstream)
	if err != nil || upstreamURL.Scheme == "" || upstreamURL.Host == "" {
		log.Error("invalid upstream", "upstream", cfg.Upstream, "err", err)
		os.Exit(1)
	}

	token, err := resolveToken(cfg.Token)
	if err != nil {
		log.Error("could not resolve token", "err", err)
		os.Exit(1)
	}

	proxy := newGitProxy(upstreamURL, token, cfg.ReadAllow, cfg.WriteAllow, log)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/_repos", reposHandler(log))
	mux.Handle("/", landingOrProxy(proxy, upstreamURL, cfg))

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           withLogging(log, mux),
		ReadHeaderTimeout: 30 * time.Second,
	}

	log.Info("broken-mirror starting",
		"addr", cfg.Addr,
		"upstream", upstreamURL.String(),
		"readable", readScopeString(cfg.ReadAllow),
		"writable", writeScopeString(cfg.WriteAllow),
	)
	fmt.Fprintf(os.Stderr, "\n  Clone a repo:  git clone http://%s/OWNER/REPO\n  List repos:    http://%s/_repos\n\n", cfg.Addr, cfg.Addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
}

// readScopeString describes the read filter for logs and the landing page.
func readScopeString(readAllow []string) string {
	if len(readAllow) == 0 {
		return "all repos (*)"
	}
	return strings.Join(readAllow, ", ")
}

// writeScopeString describes the write allowlist for logs and the landing page.
func writeScopeString(writeAllow []string) string {
	if len(writeAllow) == 0 {
		return "none (read-only)"
	}
	return strings.Join(writeAllow, ", ")
}

// landingOrProxy serves a plain-text usage page at "/" and proxies everything
// else (the git smart-HTTP repo paths) to the upstream.
func landingOrProxy(proxy http.Handler, upstream *url.URL, cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			proxy.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "broken-mirror %s\nupstream: %s\nreadable: %s\nwritable: %s\n\n",
			version, upstream.String(), readScopeString(cfg.ReadAllow), writeScopeString(cfg.WriteAllow))
		fmt.Fprintf(w, "Clone:  git clone http://%s/OWNER/REPO\n", cfg.Addr)
		fmt.Fprintf(w, "Repos:  http://%s/_repos\n", cfg.Addr)
		fmt.Fprintf(w, "Health: http://%s/healthz\n", cfg.Addr)
	}
}

// statusRecorder captures the response status for logging while preserving the
// Flusher behavior the reverse proxy relies on to stream large pack responses.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func withLogging(log *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"status", rec.status,
			"dur", time.Since(start).String(),
		)
	})
}
