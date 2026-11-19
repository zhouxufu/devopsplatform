package agent

// SQLite is used only for a one-time, read-only import of a stopped old Agent.
// New queues never create a SQLite database or use it in the collection path.
import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type queueFormat struct {
	Version        int  `json:"version"`
	LegacyImported bool `json:"legacy_imported"`
}

func (s *Spool) initialize() error {
	if _, err := os.Stat(s.dir); err == nil {
		var format queueFormat
		if err = readQueueJSON(filepath.Join(s.dir, "format.json"), &format); err != nil {
			return fmt.Errorf("queue format: %w", err)
		}
		if format.Version != 1 {
			return errors.New("unsupported agent queue format")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(s.dir), "."+filepath.Base(s.dir)+".import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	q := &Spool{path: s.path, dir: stage, nextSeq: 1, lastSync: time.Now(), sources: &Registry{states: map[string]*FileState{}}, state: queueState{Version: 1, Ack: 0, Last: 0, Sources: map[string]*FileState{}}}
	q.state.Sources = q.sources.states
	if err = writeQueueJSON(filepath.Join(stage, "checkpoint.json"), q.state); err != nil {
		return err
	}
	imported, err := q.importLegacy()
	if err != nil {
		q.release()
		return err
	}
	if err = q.Close(); err != nil {
		return err
	}
	if err = writeQueueJSON(filepath.Join(stage, "format.json"), queueFormat{1, imported}); err != nil {
		return err
	}
	if err = os.Rename(stage, s.dir); err != nil {
		return err
	}
	return syncQueueDir(filepath.Dir(s.dir))
}
func legacyQueueDB(path string) (*sql.DB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var header [16]byte
	_, err = io.ReadFull(f, header[:])
	f.Close()
	if err != nil {
		return nil, err
	}
	if string(header[:]) != "SQLite format 3\x00" {
		return nil, errors.New("existing spool is not a supported SQLite database")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := &url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}
func (s *Spool) importLegacy() (bool, error) {
	db, err := legacyQueueDB(s.path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read legacy Agent spool: %w", err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_identity'").Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		var id agentIdentity
		var aliases string
		if err = tx.QueryRow("SELECT id,aliases FROM agent_identity WHERE singleton=1").Scan(&id.ID, &aliases); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil {
			if !validPersistentID(id.ID) {
				return false, errors.New("invalid legacy Agent identity")
			}
			if err = json.Unmarshal([]byte(aliases), &id.Aliases); err != nil {
				return false, err
			}
			if err = writeQueueJSON(filepath.Join(s.dir, "identity.json"), id); err != nil {
				return false, err
			}
		}
	}
	cols := map[string]bool{}
	rows, err := tx.Query("PRAGMA table_info(spool_events)")
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var n, typ string
		var cid, notnull, pk int
		var defaultValue any
		if err = rows.Scan(&cid, &n, &typ, &notnull, &defaultValue, &pk); err != nil {
			rows.Close()
			return false, err
		}
		cols[n] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(cols) == 0 {
		return false, errors.New("legacy spool_events table is missing")
	}
	generation := "''"
	reset := "0"
	if cols["generation"] {
		generation = "generation"
	}
	if cols["reset_offset"] {
		reset = "reset_offset"
	}
	rows, err = tx.Query("SELECT event_json,source_key,source_offset,inode,mtime,head," + generation + "," + reset + " FROM spool_events ORDER BY seq")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	batch := make([]PendingEvent, 0, 500)
	size := 0
	for rows.Next() {
		var p PendingEvent
		var raw []byte
		if err = rows.Scan(&raw, &p.Key, &p.Offset, &p.Inode, &p.Mtime, &p.Head, &p.Generation, &p.Reset); err != nil {
			return false, err
		}
		if err = json.Unmarshal(raw, &p.Ev); err != nil {
			return false, fmt.Errorf("invalid legacy pending event; import stopped: %w", err)
		}
		batch = append(batch, p)
		size += len(raw)
		if len(batch) >= 500 || size >= 1<<20 {
			if err = s.EnqueueBatch(batch); err != nil {
				return false, err
			}
			batch = batch[:0]
			size = 0
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if err = s.EnqueueBatch(batch); err != nil {
		return false, err
	}
	return true, nil
}
