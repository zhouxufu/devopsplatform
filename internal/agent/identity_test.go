package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentIdentitySurvivesRenameRestartAndPreservesSpool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.db")
	s, err := OpenSpool(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{AgentID: "test/old-name", Host: "old-name", Environment: "test"}
	if err := s.EnqueueBatch([]PendingEvent{pending("preserved-event", "payload", 42)}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindIdentity(cfg); err != nil {
		t.Fatal(err)
	}
	id := cfg.AgentID
	if !validPersistentID(id) || cfg.AgentName != "test/old-name" {
		t.Fatalf("identity: %+v", cfg)
	}
	if got, err := ReadIdentity(path); err != nil || got != id {
		t.Fatalf("read identity %s %v", got, err)
	}
	s.Close()
	s, err = OpenSpool(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg = &Config{AgentID: "prod/new-name", Host: "new-name", Environment: "prod"}
	if err := s.BindIdentity(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AgentID != id || cfg.AgentName != "prod/new-name" || len(cfg.AgentAliases) != 2 {
		t.Fatalf("renamed: %+v", cfg)
	}
	rows, err := s.Fetch(10, 1<<20)
	if err != nil || len(rows) != 1 || rows[0].Pending.Ev.ID != "preserved-event" {
		t.Fatalf("spool changed: %+v %v", rows, err)
	}
	other, err := OpenSpool(filepath.Join(t.TempDir(), "spool.db"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherCfg := &Config{AgentID: cfg.AgentName}
	if err := other.BindIdentity(otherCfg); err != nil {
		t.Fatal(err)
	}
	if otherCfg.AgentID == id {
		t.Fatal("different installations must not share identity even with the same name")
	}
}

func TestCorruptIdentityFailsWithoutRotation(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool.db"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.BindIdentity(&Config{AgentID: "test/node"}); err != nil {
		t.Fatal(err)
	}
	if err := writeQueueJSON(filepath.Join(s.dir, "identity.json"), agentIdentity{ID: "broken"}); err != nil {
		t.Fatal(err)
	}

	if err := s.BindIdentity(&Config{AgentID: "test/renamed"}); err == nil {
		t.Fatal("corrupt identity silently replaced")
	}
	var stored agentIdentity
	if err := readQueueJSON(filepath.Join(s.dir, "identity.json"), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ID != "broken" {
		t.Fatal("identity rotated")
	}
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Fatal("new queue created a SQLite database")
	}
}
