package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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
