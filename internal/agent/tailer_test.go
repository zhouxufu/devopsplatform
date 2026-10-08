package agent

import (
	"regexp"
	"testing"
	"time"
)

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
