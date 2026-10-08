package agent

import (
	"errors"
	"path/filepath"
	"testing"

	"logplatform/internal/types"
)

func pending(id, msg string, offset int64) PendingEvent {
	return PendingEvent{
		Ev:  types.Event{ID: id, Ts: 1, Service: "api", Environment: "test", Message: msg},
		Key: "api|/tmp/api.log", Offset: offset, Inode: 7, Mtime: 1, Head: "head", Generation: "generation-1",
	}
}

func TestSpoolSurvivesRestartAndDeduplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.db")
	s, err := OpenSpool(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueBatch([]PendingEvent{pending("one", "a", 1), pending("two", "b", 2), pending("one", "a", 1)}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Count(); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	s.Close()

	s, err = OpenSpool(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reg := LoadRegistry(filepath.Join(t.TempDir(), "registry.json"))
	if err := s.RestoreRegistry(reg); err != nil {
		t.Fatal(err)
	}
	if offset, generation := reg.ResolveFile("api|/tmp/api.log", 7, 1, "head"); offset != 2 || generation != "generation-1" {
		t.Fatalf("restored registry = offset %d generation %q", offset, generation)
	}
	records, err := s.Fetch(1, 1<<20)
	if err != nil || len(records) != 1 || records[0].Pending.Ev.ID != "one" {
		t.Fatalf("first fetch = %#v, %v", records, err)
	}
	if err := s.Ack(records); err != nil {
		t.Fatal(err)
	}
	records, err = s.Fetch(10, 1<<20)
	if err != nil || len(records) != 1 || records[0].Pending.Ev.ID != "two" {
		t.Fatalf("remaining fetch = %#v, %v", records, err)
	}
}

func TestSpoolBackpressuresAtLimit(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.EnqueueBatch([]PendingEvent{pending("one", "larger than one byte", 1)}); !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("enqueue error = %v, want ErrSpoolFull", err)
	}
}
