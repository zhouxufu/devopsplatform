package agent

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestEventIDCompatibleAcrossAgentUpgrade(t *testing.T) {
	for _, path := range []string{"/x.log", strings.Repeat("长路径/", 160)} {
		old := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%s", "host", "prod", "api", path, "generation-1", uint64(1<<63+9), uint64(2), int64(10), int64(20), "中文\nstack")))
		if got := eventID("host", "prod", "api", path, "generation-1", 1<<63+9, 2, 10, 20, "中文\nstack"); got != fmt.Sprintf("%x", old) {
			t.Fatalf("upgrade changed event identity for path length %d", len(path))
		}
	}
}

func TestReadLineFastAndFragmentedBoundaries(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		max         int
		eof         bool
	}{
		{"中文\r\nnext\n", "中文", 32, false},
		{"last", "last", 32, true},
		{strings.Repeat("x", 200) + "\nnext\n", strings.Repeat("x", 200), 256, false},
		{strings.Repeat("x", 200) + "\nnext\n", strings.Repeat("x", 32) + " ...[truncated]", 32, false},
	} {
		r := bufio.NewReaderSize(strings.NewReader(tc.input), 16)
		got, consumed, err := readLine(r, tc.max)
		wantConsumed := len(tc.input)
		if n := strings.IndexByte(tc.input, '\n'); n >= 0 {
			wantConsumed = n + 1
		}
		if got != tc.want || consumed != int64(wantConsumed) || (err == io.EOF) != tc.eof {
			t.Fatalf("readLine: got %q %d %v; want %q %d eof=%v", got, consumed, err, tc.want, wantConsumed, tc.eof)
		}
		if !tc.eof {
			if next, _, err := readLine(r, 32); next != "next" || err != nil {
				t.Fatalf("next record lost: %q %v", next, err)
			}
		}
	}
}

func TestMergerKeepsExactPreviousEventOffset(t *testing.T) {
	m := newMerger(regexp.MustCompile(`^\d{4}-`))
	now := time.Now()
	if _, ok := m.add("2026-09-14 INFO first", now, 0, 22); ok {
		t.Fatal("first line must remain buffered")
	}
	if _, ok := m.add("stack line", now.Add(time.Millisecond), 22, 33); ok {
		t.Fatal("continuation must remain buffered")
	}
	ev, ok := m.add("2026-09-14 INFO second", now.Add(2*time.Millisecond), 33, 56)
	if !ok {
		t.Fatal("new start must flush previous event")
	}
	if ev.Start != 0 || ev.End != 33 {
		t.Fatalf("previous offsets = [%d,%d), want [0,33)", ev.Start, ev.End)
	}
	if ev.Message != "2026-09-14 INFO first\nstack line" {
		t.Fatalf("message = %q", ev.Message)
	}
	next, ok := m.flush()
	if !ok || next.Start != 33 || next.End != 56 {
		t.Fatalf("next offsets = [%d,%d), ok=%v", next.Start, next.End, ok)
	}
}

func TestEventIDStableAndContentSensitive(t *testing.T) {
	a := eventID("host", "prod", "api", "/x.log", "generation-1", 1, 2, 10, 20, "hello")
	b := eventID("host", "prod", "api", "/x.log", "generation-1", 1, 2, 10, 20, "hello")
	c := eventID("host", "prod", "api", "/x.log", "generation-1", 1, 2, 10, 20, "changed")
	if a != b || a == c || len(a) != 64 {
		t.Fatalf("unexpected IDs: %q %q %q", a, b, c)
	}
}

func TestCompatibleHeadAllowsGrowingNewFile(t *testing.T) {
	old := "aGVsbG8="           // hello
	newer := "aGVsbG8gd29ybGQ=" // hello world
	if !compatibleHead(old, newer) {
		t.Fatal("a short initial fingerprint should match its grown prefix")
	}
	if compatibleHead(old, "d29ybGQ=") {
		t.Fatal("different file head must not match")
	}
}

func TestRegistryResetsOffsetForCopytruncate(t *testing.T) {
	r := LoadRegistry(t.TempDir() + "/registry.json")
	r.Commit("api|app.log", 1000, 7, 1, "c2FtZSBoZWFk", "old-generation", false)
	r.Commit("api|app.log", 20, 7, 2, "c2FtZSBoZWFk", "new-generation", true) // same inode/head, explicit truncate generation
	if got := r.Resolve("api|app.log", 7, 2, "c2FtZSBoZWFk"); got != 20 {
		t.Fatalf("copytruncate offset = %d, want 20", got)
	}
}

func TestMergerPreservesExplicitANSIStartRule(t *testing.T) {
	m := newMerger(regexp.MustCompile(`^\x1b\[32m`))
	now := time.Now()
	first := "\x1b[32mfirst\x1b[0m"
	second := "\x1b[32msecond\x1b[0m"
	m.add(first, now, 0, 20)
	got, ok := m.add(second, now, 20, 40)
	if !ok || got.Message != first || got.End != 20 {
		t.Fatal("explicit ANSI custom rule regressed", got, ok)
	}
}
