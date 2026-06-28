package main

import (
	"fmt"
	"strings"
	"testing"
)

const (
	zeroOID = "0000000000000000000000000000000000000000"
	oneOID  = "1111111111111111111111111111111111111111"
)

// pktLine encodes s as a git pkt-line (4-hex-digit length prefix + payload).
func pktLine(s string) string {
	return fmt.Sprintf("%04x%s", len(s)+4, s)
}

// receivePackBody builds a git-receive-pack request body: command pkt-lines
// (capabilities appended to the first), a flush packet, then a dummy packfile.
func receivePackBody(cmds ...string) string {
	var b strings.Builder
	for i, c := range cmds {
		line := c
		if i == 0 {
			line += "\x00 report-status side-band-64k"
		}
		b.WriteString(pktLine(line + "\n"))
	}
	b.WriteString("0000")
	b.WriteString("PACK\x00\x00\x00\x02dummy-packfile-bytes")
	return b.String()
}

func cmd(old, new, ref string) string { return old + " " + new + " " + ref }

func TestParseReceivePackCommands(t *testing.T) {
	body := receivePackBody(
		cmd(zeroOID, oneOID, "refs/heads/main"),
		cmd(oneOID, zeroOID, "refs/tags/v1.0"),
	)
	cmds, consumed, err := parseReceivePackCommands(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2: %+v", len(cmds), cmds)
	}
	if cmds[0].ref != "refs/heads/main" || cmds[1].ref != "refs/tags/v1.0" {
		t.Fatalf("refs = %q, %q", cmds[0].ref, cmds[1].ref)
	}
	// The consumed prefix must end at the flush packet, leaving the packfile.
	if !strings.HasSuffix(string(consumed), "0000") {
		t.Fatalf("consumed prefix does not end with flush packet: %q", consumed)
	}
	if rest := body[len(consumed):]; !strings.HasPrefix(rest, "PACK") {
		t.Fatalf("remaining stream should start with PACK, got %q", rest)
	}
}

func TestRefPolicyAllow(t *testing.T) {
	rp := newRefPolicy(&WritePolicy{
		Branches: []string{"main", "release/*"},
		Tags:     []string{"v*"},
	})
	cases := []struct {
		ref  string
		want bool
	}{
		{"refs/heads/main", true},
		{"refs/heads/dev", false},
		{"refs/heads/release/1.0", true},
		{"refs/tags/v1.2.3", true},
		{"refs/tags/beta", false},
		{"refs/notes/commits", false}, // other namespaces denied under a policy
	}
	for _, tc := range cases {
		if got := rp.allow(tc.ref); got != tc.want {
			t.Errorf("allow(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

func TestRefPolicyEmptyListDenies(t *testing.T) {
	rp := newRefPolicy(&WritePolicy{Tags: []string{"v*"}}) // no branches => deny all branches
	if rp.allow("refs/heads/main") {
		t.Error("empty branches list should deny all branch pushes")
	}
	if !rp.allow("refs/tags/v9") {
		t.Error("tags should still be allowed")
	}
}
