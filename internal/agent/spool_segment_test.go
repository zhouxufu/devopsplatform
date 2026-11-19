package agent

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func noisyPending(i, size int) PendingEvent {
	r := rand.New(rand.NewSource(int64(i + 7)))
	b := make([]byte, size)
	r.Read(b)
	return pending(fmt.Sprintf("event-%d", i), base64.StdEncoding.EncodeToString(b), int64(i+1))
}
func openTestQueue(t *testing.T) (*Spool, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spool.db")
	s, err := OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}
func drainQueue(t *testing.T, s *Spool) []SpoolRecord {
	t.Helper()
	var all []SpoolRecord
	for {
		rows, err := s.Fetch(3, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			return all
		}
		all = append(all, rows...)
		if err = s.Ack(rows); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSegmentCodecPreservesAllFieldsAndRejectsTruncation(t *testing.T) {
	p := noisyPending(4, 400)
	p.Ev.Ts = -7
	p.Inode = 1<<63 + 19
	p.Ev.ApplicationID = "应用"
	p.Ev.Host = "node"
	p.Ev.Path = "/x"
	p.Ev.Level = "ERROR"
	p.Ev.TraceID = "trace"
	p.Ev.Message = "中文\x00\nstack\r\n"
	p.Reset = true
	b, err := encodeQueueBatch([]PendingEvent{p})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := decodeQueueBatch(b, 1, 11)
	if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0].Pending, p) {
		t.Fatalf("round trip: %+v %v", rows, err)
	}
	for n := 0; n < len(b); n++ {
		if _, err = decodeQueueBatch(b[:n], 1, 11); err == nil {
			t.Fatalf("accepted truncated record at %d", n)
		}
	}
}
func TestSegmentLockAndReadOnlyIdentity(t *testing.T) {
	s, path := openTestQueue(t)
	defer s.Close()
	c := &Config{AgentID: "test/node"}
	if err := s.BindIdentity(c); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(path, 32<<20); err == nil {
		t.Fatal("two writers opened one queue")
	}
	if got, err := ReadIdentity(path); err != nil || got != c.AgentID {
		t.Fatalf("read identity %s %v", got, err)
	}
}
func TestSegmentRejectsOutOfOrderAckAndPersistsPartialAck(t *testing.T) {
	s, path := openTestQueue(t)
	if err := s.EnqueueBatch([]PendingEvent{pending("a", "first", 1), pending("b", "second", 2), pending("c", "third", 3)}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Fetch(3, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Ack(rows[1:]); err == nil {
		t.Fatal("out-of-order ACK accepted")
	}
	if n, _ := s.Count(); n != 3 {
		t.Fatal("invalid ACK changed queue")
	}
	if err = s.Ack(rows[:1]); err != nil {
		t.Fatal(err)
	}
	if err = s.Ack(rows[:1]); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rest := drainQueue(t, s)
	if len(rest) != 2 || rest[0].Pending.Ev.ID != "b" {
		t.Fatalf("partial ACK restart: %+v", rest)
	}
}
func TestSegmentRotationReclamationAndSourceCheckpoint(t *testing.T) {
	s, path := openTestQueue(t)
	s.segmentBytes = 700
	for i := 0; i < 12; i++ {
		if err := s.EnqueueBatch([]PendingEvent{noisyPending(i, 320)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.segments) < 3 {
		t.Fatal("fixture did not rotate")
	}
	all := drainQueue(t, s)
	if len(all) != 12 {
		t.Fatalf("got %d records", len(all))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(path + ".queue/segment-*.qlog")
	if len(files) != 0 {
		t.Fatalf("acknowledged segments retained: %v", files)
	}
	s, err := OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reg := LoadRegistry(filepath.Join(t.TempDir(), "lost-registry.json"))
	if err = s.RestoreRegistry(reg); err != nil {
		t.Fatal(err)
	}
	if off, gen := reg.ResolveFile("api|/tmp/api.log", 7, 1, "head"); off != 12 || gen != "generation-1" {
		t.Fatalf("source checkpoint lost after reclamation: %d %s", off, gen)
	}
	if err = s.EnqueueBatch([]PendingEvent{pending("next", "next", 13)}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Fetch(1, 1<<20)
	if err != nil || rows[0].Seq <= 12 {
		t.Fatalf("sequence reused: %+v %v", rows, err)
	}
}
func TestSegmentFullNeverAdvancesSourceOrDiscardsPending(t *testing.T) {
	s, _ := openTestQueue(t)
	defer s.Close()
	s.maxBytes = 850
	if err := s.EnqueueBatch([]PendingEvent{noisyPending(0, 400)}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Bytes()
	if err := s.EnqueueBatch([]PendingEvent{noisyPending(1, 400)}); !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("full error=%v", err)
	}
	if n, _ := s.Count(); n != 1 {
		t.Fatal("full queue lost or added data")
	}
	if off, _ := s.sources.ResolveFile("api|/tmp/api.log", 7, 1, "head"); off != 1 {
		t.Fatal("full queue advanced source")
	}
	if after, _ := s.Bytes(); after != before {
		t.Fatal("failed append changed byte accounting")
	}
	drainQueue(t, s)
	if err := s.EnqueueBatch([]PendingEvent{noisyPending(1, 400)}); err != nil {
		t.Fatalf("queue did not resume: %v", err)
	}
}
func TestSegmentCorruptionRetainsOriginalAndValidSuffix(t *testing.T) {
	s, path := openTestQueue(t)
	for i := 0; i < 3; i++ {
		if err := s.EnqueueBatch([]PendingEvent{noisyPending(i, 100)}); err != nil {
			t.Fatal(err)
		}
	}
	file := s.segmentPath(s.segments[0].id)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	b[frameHeaderSize+8] ^= 0x40
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenSpool(path, 32<<20); !errors.Is(err, ErrSpoolCorrupt) {
		t.Fatalf("corruption not rejected: %v", err)
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(b, after) {
		t.Fatal("corrupt frame or valid suffix was discarded")
	}
}
func TestSegmentMetadataFailureDoesNotReclaimData(t *testing.T) {
	s, path := openTestQueue(t)
	if err := s.EnqueueBatch([]PendingEvent{pending("a", "body", 1)}); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Fetch(1, 1<<20)
	if err := os.Mkdir(filepath.Join(s.dir, "checkpoint.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	s.lastSync = time.Now().Add(-time.Minute)
	if err := s.Ack(rows); err == nil {
		t.Fatal("metadata failure accepted")
	}
	if _, err := os.Stat(s.segmentPath(s.segments[0].id)); err != nil {
		t.Fatal("data reclaimed without checkpoint")
	}
	s.release()
	os.Remove(filepath.Join(s.dir, "checkpoint.json.tmp"))
	again, err := OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if n, _ := again.Count(); n != 1 {
		t.Fatal("failed metadata commit lost replay")
	}
}
func makeLegacyQueue(t *testing.T, path string, count int) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE spool_events(seq INTEGER PRIMARY KEY,event_json BLOB,source_key TEXT,source_offset INTEGER,inode INTEGER,mtime INTEGER,head TEXT,generation TEXT,reset_offset INTEGER);CREATE TABLE agent_identity(singleton INTEGER PRIMARY KEY,id TEXT,aliases TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	id := "agent_0123456789abcdef0123456789abcdef"
	if _, err = db.Exec(`INSERT INTO agent_identity VALUES(1,?,?)`, id, `["test/legacy"]`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		p := pending(fmt.Sprintf("legacy-%d", i), "中文\nstack", int64(i+1))
		b, _ := json.Marshal(p.Ev)
		if _, err = tx.Exec(`INSERT INTO spool_events VALUES(?,?,?,?,?,?,?,?,?)`, i+1, b, p.Key, p.Offset, p.Inode, p.Mtime, p.Head, p.Generation, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestSegmentImportsLegacyOnceAndKeepsDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	id := makeLegacyQueue(t, path, 503)
	before, _ := os.ReadFile(path)
	s, err := OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	c := &Config{AgentID: "test/new-name"}
	if err = s.BindIdentity(c); err != nil || c.AgentID != id {
		t.Fatalf("identity import: %+v %v", c, err)
	}
	rows := drainQueue(t, s)
	if len(rows) != 503 || rows[0].Pending.Ev.ID != "legacy-0" || rows[502].Pending.Ev.ID != "legacy-502" {
		t.Fatalf("legacy import count=%d", len(rows))
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("legacy database modified")
	}
	if err = os.WriteFile(path, []byte("retained legacy file unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.Count(); n != 0 {
		t.Fatal("legacy database reimported")
	}
}
func TestSegmentFailedLegacyImportIsNotPublished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	makeLegacyQueue(t, path, 502)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE spool_events SET event_json='broken' WHERE seq=502`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = OpenSpool(path, 32<<20); err == nil {
		t.Fatal("bad legacy JSON accepted")
	}
	if _, err = os.Stat(path + ".queue"); !os.IsNotExist(err) {
		t.Fatal("partial import published")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(pending("legacy-501", "repaired", 502).Ev)
	_, err = db.Exec(`UPDATE spool_events SET event_json=? WHERE seq=502`, b)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.Count(); n != 502 {
		t.Fatalf("retry import duplicated prefix: %d", n)
	}
}
func TestSegmentCrashHelper(t *testing.T) {
	mode := os.Getenv("SEGMENT_CRASH_MODE")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	path := os.Getenv("SEGMENT_CRASH_PATH")
	s, err := OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnqueueBatch([]PendingEvent{pending("a", "中文\nstack", 1), pending("b", "second", 2), pending("c", "third", 3)}); err != nil {
		t.Fatal(err)
	}
	if mode == "ack" || mode == "reclaimed" {
		rows, err := s.Fetch(3, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "ack" {
			rows = rows[:1]
		}
		if err = s.Ack(rows); err != nil {
			t.Fatal(err)
		}
		if mode == "reclaimed" {
			if err = s.checkpoint(true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "torn" {
		file := s.segmentPath(s.segments[0].id)
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		off := s.segments[0].size
		if _, err = s.writer.WriteAt(b[:frameHeaderSize+4], off); err != nil {
			t.Fatal(err)
		}
		s.writer.Sync()
	}
	if err = os.WriteFile(path+".ready", []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Hour)
}
func TestSegmentSurvivesRealSIGKILL(t *testing.T) {
	for _, mode := range []string{"pending", "ack", "reclaimed", "torn"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "spool.db")
			cmd := exec.Command(os.Args[0], "-test.run=^TestSegmentCrashHelper$")
			cmd.Env = append(os.Environ(), "SEGMENT_CRASH_MODE="+mode, "SEGMENT_CRASH_PATH="+path)
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cmd.Process.Kill(); cmd.Wait() }()
			ready := false
			for until := time.Now().Add(8 * time.Second); time.Now().Before(until); {
				if _, err := os.Stat(path + ".ready"); err == nil {
					ready = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !ready {
				t.Fatalf("child not ready: %s", output.String())
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			cmd.Wait()
			s, err := OpenSpool(path, 32<<20)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			want := 3
			if mode == "reclaimed" {
				want = 0
			}
			if n, _ := s.Count(); n != want {
				t.Fatalf("mode=%s count=%d want=%d", mode, n, want)
			}
			reg := LoadRegistry(filepath.Join(t.TempDir(), "registry.json"))
			s.RestoreRegistry(reg)
			if off, _ := reg.ResolveFile("api|/tmp/api.log", 7, 1, "head"); off != 3 {
				t.Fatalf("lost source checkpoint: %d", off)
			}
			if mode == "torn" {
				files, _ := filepath.Glob(filepath.Join(s.dir, "quarantine", "*.bin"))
				if len(files) != 1 {
					t.Fatal("torn tail evidence missing")
				}
			}
			if want > 0 {
				rows := drainQueue(t, s)
				if len(rows) != want || rows[0].Pending.Ev.Message != "中文\nstack" {
					t.Fatal("replay changed event identity/content")
				}
			}
		})
	}
}

func TestSegmentConcurrentWritersPartialReadsAndCapacity(t *testing.T) {
	s, _ := openTestQueue(t)
	defer s.Close()
	s.segmentBytes = 1500
	s.maxBytes = 24000
	const writers = 4
	const each = 80
	errs := make(chan error, writers)
	done := make(chan struct{})
	for w := 0; w < writers; w++ {
		go func(w int) {
			var failure error
			defer func() { errs <- failure }()
			for i := 0; i < each; i += 8 {
				batch := make([]PendingEvent, 0, 8)
				for j := i; j < i+8; j++ {
					p := noisyPending(w*each+j, 100)
					p.Key = fmt.Sprintf("source-%d", w)
					p.Offset = int64(j + 1)
					batch = append(batch, p)
				}
				for {
					err := s.EnqueueBatch(batch)
					if err == nil {
						break
					}
					if !errors.Is(err, ErrSpoolFull) {
						failure = err
						return
					}
					select {
					case <-done:
						return
					case <-time.After(time.Millisecond):
					}
				}
			}
		}(w)
	}
	defer close(done)
	seen := map[string]bool{}
	deadline := time.Now().Add(15 * time.Second)
	for len(seen) < writers*each && time.Now().Before(deadline) {
		rows, err := s.Fetch(3, 1200)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if seen[row.Pending.Ev.ID] {
				t.Fatal("queue repeated without restart")
			}
			seen[row.Pending.Ev.ID] = true
		}
		if err = s.Ack(rows); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if len(seen) != writers*each {
		t.Fatalf("concurrent queue drained %d/%d", len(seen), writers*each)
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := s.Bytes(); n != 0 {
		t.Fatalf("pending byte count after drain=%d", n)
	}
	if s.diskBytes > s.maxBytes {
		t.Fatalf("segment quota exceeded: %d", s.diskBytes)
	}
}
func TestSegmentAppendFailureDoesNotAdvanceCheckpoint(t *testing.T) {
	s, path := openTestQueue(t)
	if err := s.EnqueueBatch([]PendingEvent{pending("first", "committed", 1)}); err != nil {
		t.Fatal(err)
	}
	s.writer.Close()
	if err := s.EnqueueBatch([]PendingEvent{pending("second", "not committed", 2)}); err == nil {
		t.Fatal("append to closed file succeeded")
	}
	if off, _ := s.sources.ResolveFile("api|/tmp/api.log", 7, 1, "head"); off != 1 {
		t.Fatal("failed append advanced source")
	}
	s.Close()
	again, err := OpenSpool(path, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if n, _ := again.Count(); n != 1 {
		t.Fatalf("failed append changed recovery count: %d", n)
	}
}
func TestSegmentMissingFileAndCorruptCheckpointFailClosed(t *testing.T) {
	for _, mode := range []string{"missing", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			s, path := openTestQueue(t)
			s.segmentBytes = 200
			for i := 0; i < 3; i++ {
				if err := s.EnqueueBatch([]PendingEvent{noisyPending(i, 100)}); err != nil {
					t.Fatal(err)
				}
			}
			files := append([]queueSegment(nil), s.segments...)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "missing" {
				if err := os.Remove(s.segmentPath(files[1].id)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(s.dir, "checkpoint.json"), []byte(`{"data":{"ack":999},"sha256":"wrong"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := OpenSpool(path, 32<<20); err == nil {
				t.Fatal("missing/corrupt state silently accepted")
			}
			if _, err := os.Stat(s.segmentPath(files[2].id)); err != nil {
				t.Fatal("valid suffix deleted on failure")
			}
		})
	}
}

func TestSegmentCleanupRetriesWithoutDoubleCounting(t *testing.T) {
	s, _ := openTestQueue(t)
	defer s.Close()
	s.segmentBytes = 200
	for i := 0; i < 3; i++ {
		if err := s.EnqueueBatch([]PendingEvent{noisyPending(i, 100)}); err != nil {
			t.Fatal(err)
		}
	}
	drainQueue(t, s)
	if len(s.segments) != 3 {
		t.Fatal("fixture did not create three segments")
	}
	blocked := s.segmentPath(s.segments[1].id)
	held := blocked + ".held"
	if err := os.Rename(blocked, held); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "block"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.checkpoint(true); err == nil {
		t.Fatal("unlink failure not returned")
	}
	if len(s.segments) != 2 {
		t.Fatal("completed cleanup not retained in accounting")
	}
	if err := os.RemoveAll(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(held, blocked); err != nil {
		t.Fatal(err)
	}
	if err := s.checkpoint(true); err != nil {
		t.Fatal(err)
	}
	if s.diskBytes != 0 || len(s.segments) != 0 {
		t.Fatalf("cleanup did not resume: bytes=%d segments=%d", s.diskBytes, len(s.segments))
	}
}
func TestSegmentRepeatedIncompleteTailsKeepDistinctEvidence(t *testing.T) {
	s, path := openTestQueue(t)
	if err := s.EnqueueBatch([]PendingEvent{pending("first", "committed", 1)}); err != nil {
		t.Fatal(err)
	}
	seg := s.segments[0]
	s.Close()
	for i := 0; i < 2; i++ {
		f, err := os.OpenFile(s.segmentPath(seg.id), os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte{byte(i), 7, 8})
		f.Sync()
		f.Close()
		s, err = OpenSpool(path, 32<<20)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(path+".queue", "quarantine", "*.bin"))
	if len(files) != 2 {
		t.Fatalf("tail evidence overwritten: %v", files)
	}
}
