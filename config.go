package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds every runtime setting. It is loaded from a TOML file; CLI flags
// may override individual fields.
type Config struct {
	// Addr is the listen address. Loopback by default.
	Addr string `toml:"addr"`
	// Upstream is the git host to proxy to.
	Upstream string `toml:"upstream"`
	// Token optionally pins the upstream PAT. If empty, it is resolved from
	// GH_TOKEN / GITHUB_TOKEN / `gh auth token` at startup.
	Token string `toml:"token"`
	// ReadAllow is a list of glob patterns ("OWNER/REPO", "OWNER/*", "*") naming
	// the repos that may be read (cloned/fetched). Empty/absent means all repos
	// the token can reach. Wildcards are allowed.
	ReadAllow []string `toml:"read_allow"`
	// WriteAllow is the explicit list of "OWNER/REPO" names that may be pushed
	// to. Everything else is read-only. No wildcards.
	WriteAllow []string `toml:"write_allow"`
	// WritePolicy, when set, restricts which refs a push may update across all
	// writable repos. A nil pointer (no [write_policy] table) means no ref-level
	// restriction.
	WritePolicy *WritePolicy `toml:"write_policy"`
}

// WritePolicy restricts pushes to refs whose name matches the given glob
// patterns. branches patterns are matched against branch names (the part after
// "refs/heads/") and tags against tag names ("refs/tags/"). An empty list for a
// ref type denies that type entirely; use ["*"] to allow all.
type WritePolicy struct {
	Branches []string `toml:"branches"`
	Tags     []string `toml:"tags"`
}

func defaultConfig() Config {
	return Config{
		Addr:     "127.0.0.1:8080",
		Upstream: "https://github.com",
	}
}

// defaultConfigPath is the file loadConfig reads when no path is given.
const defaultConfigPath = "broken-mirror.toml"

// loadConfig returns the configuration, starting from built-in defaults and
// overlaying a TOML file. If path is empty it looks for ./broken-mirror.toml and
// treats its absence as "use defaults"; if path is set explicitly, a missing
// file is an error.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	explicit := path != ""
	if !explicit {
		path = defaultConfigPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config %s: %w", path, err)
	}

	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if err := validateReadAllow(cfg.ReadAllow); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if err := validateWriteAllow(cfg.WriteAllow); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if err := validateWritePolicy(cfg.WritePolicy); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// validateWritePolicy checks that branch/tag patterns are non-empty strings.
// Globs are allowed, so the rules are intentionally light.
func validateWritePolicy(wp *WritePolicy) error {
	if wp == nil {
		return nil
	}
	for _, group := range []struct {
		name     string
		patterns []string
	}{{"branches", wp.Branches}, {"tags", wp.Tags}} {
		for _, p := range group.patterns {
			if strings.TrimSpace(p) == "" {
				return fmt.Errorf("write_policy.%s contains an empty pattern", group.name)
			}
		}
	}
	return nil
}

// validateReadAllow checks that each read_allow pattern is non-empty. Wildcards
// are allowed here (unlike write_allow), so the rules are intentionally light.
func validateReadAllow(list []string) error {
	for _, e := range list {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("read_allow contains an empty pattern")
		}
	}
	return nil
}

// validateWriteAllow enforces that every write_allow entry is an explicit
// "OWNER/REPO" name — no wildcards, no globs, no bare owners.
func validateWriteAllow(list []string) error {
	for _, e := range list {
		if strings.ContainsAny(e, "*?[]") {
			return fmt.Errorf("write_allow entry %q contains a wildcard; only explicit OWNER/REPO names are allowed", e)
		}
		if strings.Count(e, "/") != 1 || strings.HasPrefix(e, "/") || strings.HasSuffix(e, "/") {
			return fmt.Errorf("write_allow entry %q must be exactly OWNER/REPO", e)
		}
	}
	return nil
}
