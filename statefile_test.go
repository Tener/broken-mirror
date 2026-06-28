package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mirror.json")
	want := State{
		PID: 4242, Addr: "127.0.0.1:54321", Upstream: "https://github.com",
		ReadAllow: []string{"*"}, WriteAllow: []string{}, StartedAt: "2026-06-28T14:40:00Z",
	}
	if err := writeState(path, want); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PID != want.PID || got.Addr != want.Addr || got.Upstream != want.Upstream || got.StartedAt != want.StartedAt {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if len(got.ReadAllow) != 1 || got.ReadAllow[0] != "*" {
		t.Fatalf("read_allow = %v", got.ReadAllow)
	}
}

func TestRemoveStateIsQuietWhenAbsent(t *testing.T) {
	removeState(filepath.Join(t.TempDir(), "does-not-exist.json")) // must not panic
}
