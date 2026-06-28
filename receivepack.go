package main

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// refUpdate is one command from a git-receive-pack request: a request to move
// ref from old to new. A zero old means create; a zero new means delete.
type refUpdate struct {
	old string
	new string
	ref string // full ref name, e.g. "refs/heads/main"
}

// parseReceivePackCommands reads the pkt-line command section at the start of a
// git-receive-pack request body and returns the ref updates it found. It also
// returns the exact bytes it consumed (commands plus the terminating flush
// packet) so the caller can replay them ahead of the untouched packfile.
//
// Each command pkt-line is "<old-sha> <new-sha> <ref>", with a NUL-separated
// capability list appended to the first line. The command section ends at the
// first flush packet ("0000"); everything after it (push-options, the packfile)
// is left unread in r.
func parseReceivePackCommands(r io.Reader) (cmds []refUpdate, consumed []byte, err error) {
	var buf bytes.Buffer
	tee := io.TeeReader(r, &buf)
	first := true

	for {
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(tee, hdr); err != nil {
			return cmds, buf.Bytes(), fmt.Errorf("reading pkt-line length: %w", err)
		}
		if string(hdr) == "0000" { // flush packet: end of commands
			break
		}
		n, err := strconv.ParseUint(string(hdr), 16, 32)
		if err != nil || n < 4 {
			return cmds, buf.Bytes(), fmt.Errorf("invalid pkt-line length %q", hdr)
		}
		payload := make([]byte, n-4)
		if _, err := io.ReadFull(tee, payload); err != nil {
			return cmds, buf.Bytes(), fmt.Errorf("reading pkt-line payload: %w", err)
		}

		line := strings.TrimRight(string(payload), "\n")
		if first {
			if i := strings.IndexByte(line, 0); i >= 0 {
				line = line[:i] // drop the capability list
			}
			first = false
		}
		if f := strings.Fields(line); len(f) >= 3 {
			cmds = append(cmds, refUpdate{old: f[0], new: f[1], ref: f[2]})
		}
	}
	return cmds, buf.Bytes(), nil
}

// refPolicy decides whether a push that updates a given ref is permitted, based
// on branch and tag name glob patterns. A nil refPolicy means no ref-level
// restriction.
type refPolicy struct {
	branches *repoMatcher
	tags     *repoMatcher
}

func newRefPolicy(wp *WritePolicy) *refPolicy {
	if wp == nil {
		return nil
	}
	return &refPolicy{
		branches: newRefMatcher(wp.Branches),
		tags:     newRefMatcher(wp.Tags),
	}
}

// allow reports whether updating fullRef is permitted. Only branches
// (refs/heads/*) and tags (refs/tags/*) are governed; any other ref namespace
// is denied while a policy is in force.
func (rp *refPolicy) allow(fullRef string) bool {
	if name, ok := strings.CutPrefix(fullRef, "refs/heads/"); ok {
		return rp.branches.match(name)
	}
	if name, ok := strings.CutPrefix(fullRef, "refs/tags/"); ok {
		return rp.tags.match(name)
	}
	return false
}
