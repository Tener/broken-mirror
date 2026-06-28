package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State is the runtime readout broken-mirror writes to --state-file at startup:
// the pid and the actual bound address (after binding 127.0.0.1:0 to a random
// port), plus the effective scope. A supervisor reads it to learn the port and
// to check liveness, then removes it (or it is removed on graceful shutdown).
type State struct {
	PID        int      `json:"pid"`
	Addr       string   `json:"addr"`
	Upstream   string   `json:"upstream"`
	ReadAllow  []string `json:"read_allow"`
	WriteAllow []string `json:"write_allow"`
	StartedAt  string   `json:"started_at"`
}

// writeState atomically writes s as pretty JSON to path with mode 0600, via a
// temp file in the same directory followed by rename.
func writeState(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mirror-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// removeState deletes the state file, ignoring "not found".
func removeState(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
