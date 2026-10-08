package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// ErrSpoolFull 表示本地持久队列达到上限。调用方必须背压等待，不能丢弃事件。
var ErrSpoolFull = errors.New("agent spool is full")

// SpoolRecord 是已经可靠落盘、等待服务端 ACK 的事件。
type SpoolRecord struct {
	Seq     int64
	Bytes   int64
	Pending PendingEvent
}

// Spool 使用 SQLite WAL 作为轻量本地持久队列。写入为顺序追加，ACK 后批量删除。
// synchronous=NORMAL 将 fsync 合并到 WAL checkpoint，避免每行日志同步刷盘。
type Spool struct {
	db       *sql.DB
	path     string
	maxBytes int64
}

func OpenSpool(path string, maxBytes int64) (*Spool, error) {
	if path == "" {
		return nil, errors.New("spool path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=wal_autocheckpoint(1000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS spool_events (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id TEXT NOT NULL UNIQUE,
		event_json BLOB NOT NULL,
		source_key TEXT NOT NULL,
		source_offset INTEGER NOT NULL,
		inode INTEGER NOT NULL,
		mtime INTEGER NOT NULL,
		head TEXT NOT NULL,
		generation TEXT NOT NULL DEFAULT '',
		reset_offset INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS spool_meta (
		key TEXT PRIMARY KEY,
		value INTEGER NOT NULL
	);
	INSERT OR IGNORE INTO spool_meta(key,value) VALUES ('queued_bytes',0);
	UPDATE spool_meta SET value=(SELECT COALESCE(SUM(LENGTH(event_json)),0) FROM spool_events) WHERE key='queued_bytes'`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create spool: %w", err)
	}
	// 在线兼容本功能启用后、升级前短暂创建的 spool 文件。
	_, _ = db.Exec(`ALTER TABLE spool_events ADD COLUMN generation TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE spool_events ADD COLUMN reset_offset INTEGER NOT NULL DEFAULT 0`)
	return &Spool{db: db, path: path, maxBytes: maxBytes}, nil
}

func (s *Spool) Close() error { return s.db.Close() }

// EnqueueBatch 先在一个事务中持久化整批；调用成功后才允许推进文件 registry。
func (s *Spool) EnqueueBatch(batch []PendingEvent) error {
	if len(batch) == 0 {
		return nil
	}
	payloads := make([][]byte, len(batch))
	for i := range batch {
		b, err := json.Marshal(batch[i].Ev)
		if err != nil {
			return err
		}
		payloads[i] = b
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO spool_events
		(event_id,event_json,source_key,source_offset,inode,mtime,head,generation,reset_offset)
		VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	var added int64
	for i, p := range batch {
		res, err := stmt.Exec(p.Ev.ID, payloads[i], p.Key, p.Offset, p.Inode, p.Mtime, p.Head, p.Generation, p.Reset)
		if err != nil {
			return err
		}
		if changed, _ := res.RowsAffected(); changed > 0 {
			added += int64(len(payloads[i]))
		}
	}
	var used int64
	if err := tx.QueryRow(`SELECT value FROM spool_meta WHERE key='queued_bytes'`).Scan(&used); err != nil {
		return err
	}
	if s.maxBytes > 0 && used+added > s.maxBytes {
		return ErrSpoolFull
	}
	if _, err := tx.Exec(`UPDATE spool_meta SET value=value+? WHERE key='queued_bytes'`, added); err != nil {
		return err
	}
	return tx.Commit()
}

// Fetch 返回最早的一批，限制条数和未压缩字节数。
func (s *Spool) Fetch(maxCount, maxBytes int) ([]SpoolRecord, error) {
	rows, err := s.db.Query(`SELECT seq,event_json,LENGTH(event_json),source_key,source_offset,inode,mtime,head,generation,reset_offset
		FROM spool_events ORDER BY seq LIMIT ?`, maxCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SpoolRecord, 0, maxCount)
	size := 0
	for rows.Next() {
		var rec SpoolRecord
		var raw []byte
		if err := rows.Scan(&rec.Seq, &raw, &rec.Bytes, &rec.Pending.Key, &rec.Pending.Offset,
			&rec.Pending.Inode, &rec.Pending.Mtime, &rec.Pending.Head, &rec.Pending.Generation, &rec.Pending.Reset); err != nil {
			return nil, err
		}
		if len(result) > 0 && size+len(raw) > maxBytes {
			break
		}
		if err := json.Unmarshal(raw, &rec.Pending.Ev); err != nil {
			return nil, fmt.Errorf("decode spool seq %d: %w", rec.Seq, err)
		}
		result = append(result, rec)
		size += len(raw)
	}
	return result, rows.Err()
}

// Ack 删除服务端已确认的连续前缀。
func (s *Spool) Ack(records []SpoolRecord) error {
	if len(records) == 0 {
		return nil
	}
	var removed int64
	for i := range records {
		removed += records[i].Bytes
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM spool_events WHERE seq <= ?`, records[len(records)-1].Seq); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE spool_meta SET value=MAX(0,value-?) WHERE key='queued_bytes'`, removed); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Spool) Bytes() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT value FROM spool_meta WHERE key='queued_bytes'`).Scan(&n)
	return n, err
}

func (s *Spool) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM spool_events`).Scan(&n)
	return n, err
}

// RestoreRegistry 修复“spool 事务已提交、registry 尚未来得及保存”这一极短崩溃窗口。
// 按队列顺序重放文件断点元数据，不发送也不删除任何事件。
func (s *Spool) RestoreRegistry(reg *Registry) error {
	rows, err := s.db.Query(`SELECT source_key,source_offset,inode,mtime,head,generation,reset_offset
		FROM spool_events ORDER BY seq`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p PendingEvent
		if err := rows.Scan(&p.Key, &p.Offset, &p.Inode, &p.Mtime, &p.Head, &p.Generation, &p.Reset); err != nil {
			return err
		}
		reg.Commit(p.Key, p.Offset, p.Inode, p.Mtime, p.Head, p.Generation, p.Reset)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	reg.Save()
	return nil
}
