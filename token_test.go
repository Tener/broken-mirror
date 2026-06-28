package main

import (
	"errors"
	"testing"
	"time"
)

func TestTokenProviderRefreshesAfterTTL(t *testing.T) {
	calls := 0
	resolve := func(string) (string, error) {
		calls++
		return "tok-v" + string(rune('0'+calls)), nil
	}
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }

	tp, err := newTokenProviderWith("", time.Minute, resolve, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tp.get(); got != "tok-v1" {
		t.Fatalf("initial token = %q, want tok-v1", got)
	}
	// Within TTL: cached, no new resolve.
	now = now.Add(30 * time.Second)
	if got := tp.get(); got != "tok-v1" {
		t.Fatalf("within TTL token = %q, want tok-v1 (cached)", got)
	}
	// Past TTL: re-resolves.
	now = now.Add(31 * time.Second)
	if got := tp.get(); got != "tok-v2" {
		t.Fatalf("after TTL token = %q, want tok-v2 (refreshed)", got)
	}
}

func TestTokenProviderKeepsPreviousOnRefreshError(t *testing.T) {
	first := true
	resolve := func(string) (string, error) {
		if first {
			first = false
			return "good", nil
		}
		return "", errors.New("gh unavailable")
	}
	now := time.Unix(0, 0)
	tp, err := newTokenProviderWith("", time.Minute, resolve, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute) // force a refresh that fails
	if got := tp.get(); got != "good" {
		t.Fatalf("token = %q, want previous value 'good' kept on refresh error", got)
	}
}

func TestTokenProviderInitialErrorPropagates(t *testing.T) {
	resolve := func(string) (string, error) { return "", errors.New("no token") }
	if _, err := newTokenProviderWith("", time.Minute, resolve, time.Now, nil); err == nil {
		t.Fatal("expected error when initial resolve fails")
	}
}
