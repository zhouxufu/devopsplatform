package agent

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var ErrSpoolFull = errors.New("agent spool is full")
var ErrSpoolCorrupt = errors.New("agent spool is corrupt; original files retained")

const (
	segmentTarget   = 16 << 20
	frameHeaderSize = 32
	maxFrameBytes   = 8 << 20
	maxFrameEvents  = 4096
)

type SpoolRecord struct {
	Seq     int64
	Bytes   int64
	Pending PendingEvent
}
type queueSegment struct {
	id   uint64
	size int64
	last int64
}
type queuePosition struct {
	segment uint64
	offset  int64
}
type queueState struct {
	Version int                   `json:"version"`
	Ack     int64                 `json:"ack"`
	Last    int64                 `json:"last"`
	Sources map[string]*FileState `json:"sources"`
}

// Spool appends checksummed batches and syncs them before source offsets advance.
// ACK checkpoints are coalesced; a crash can replay acknowledged records. Files
// are reclaimed only after the ACK and corresponding source offsets are synced.
type Spool struct {
	mu            sync.Mutex
	path, dir     string
	maxBytes      int64
	segmentBytes  int64
	lock, writer  *os.File
	segments      []queueSegment
	diskBytes     int64
	pending       int
	pendingBytes  int64
	nextSeq       int64
	state         queueState
	sources       *Registry
	head          queuePosition
	cache         []SpoolRecord
	cacheEnd      int64
	compressed    bytes.Buffer
	compressor    *gzip.Writer
	lastSync      time.Time
	dirty, closed bool
	poison        error
}

func OpenSpool(path string, maxBytes int64) (*Spool, error) {
	if path == "" {
		return nil, errors.New("spool path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".queue.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("agent queue is already in use: %w", err)
	}
	s := &Spool{path: path, dir: path + ".queue", maxBytes: maxBytes, lock: lock}
	if err = s.initialize(); err == nil {
		err = s.recover()
	}
	if err != nil {
		s.release()
		return nil, err
	}
	return s, nil
}
func (s *Spool) segmentPath(id uint64) string {
	return filepath.Join(s.dir, fmt.Sprintf("segment-%020d.qlog", id))
}
func (s *Spool) recover() error {
	if err := readQueueJSON(filepath.Join(s.dir, "checkpoint.json"), &s.state); err != nil {
		return fmt.Errorf("queue checkpoint: %w", err)
	}
	if s.state.Version != 1 || s.state.Ack < 0 || s.state.Last < s.state.Ack || s.state.Sources == nil {
		return ErrSpoolCorrupt
	}
	for _, source := range s.state.Sources {
		if source == nil || source.Offset < 0 {
			return ErrSpoolCorrupt
		}
	}
	s.sources = &Registry{states: s.state.Sources}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "segment-") || !strings.HasSuffix(name, ".qlog") {
			continue
		}
		id, e := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(name, "segment-"), ".qlog"), 10, 64)
		if e != nil || name != fmt.Sprintf("segment-%020d.qlog", id) || entry.IsDir() {
			return ErrSpoolCorrupt
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		s.segments = append(s.segments, queueSegment{id: id, size: info.Size()})
	}
	sort.Slice(s.segments, func(i, j int) bool { return s.segments[i].id < s.segments[j].id })
	last := int64(0)
	for i := range s.segments {
		seg := &s.segments[i]
		f, e := os.OpenFile(s.segmentPath(seg.id), os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		var off int64
		for off < seg.size {
			records, end, e := readQueueFrame(f, off, seg.size)
			if errors.Is(e, io.ErrUnexpectedEOF) && i == len(s.segments)-1 {
				e = s.preserveTail(f, seg.id, off, seg.size)
				if e == nil {
					e = f.Truncate(off)
				}
				if e == nil {
					e = f.Sync()
				}
				if e != nil {
					f.Close()
					return e
				}
				seg.size = off
				break
			}
			if e != nil {
				f.Close()
				return fmt.Errorf("%w at segment %d offset %d: %v", ErrSpoolCorrupt, seg.id, off, e)
			}
			first := records[0].Seq
			if off == 0 && uint64(first) != seg.id {
				f.Close()
				return fmt.Errorf("%w: segment sequence mismatch", ErrSpoolCorrupt)
			}
			if (last > 0 && first != last+1 && first > s.state.Ack+1) || (last == 0 && first > s.state.Ack+1) {
				f.Close()
				return fmt.Errorf("%w: missing queue prefix", ErrSpoolCorrupt)
			}
			for _, rec := range records {
				if rec.Seq > s.state.Last {
					s.commitSource(rec.Pending)
				}
				if rec.Seq > s.state.Ack {
					if s.pending == 0 {
						s.head = queuePosition{seg.id, off}
					}
					s.pending++
					s.pendingBytes += rec.Bytes
				}
			}
			last = records[len(records)-1].Seq
			seg.last = last
			off = end
		}
		f.Close()
		s.diskBytes += seg.size
	}
	if last < s.state.Last && s.state.Ack < s.state.Last {
		return fmt.Errorf("%w: checkpoint references missing pending data", ErrSpoolCorrupt)
	}
	evidence, err := os.ReadDir(filepath.Join(s.dir, "quarantine"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range evidence {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrSpoolCorrupt
		}
		s.diskBytes += info.Size()
	}
	s.nextSeq = max(last, s.state.Last) + 1
	if last > s.state.Last {
		s.dirty = true
	}
	s.lastSync = time.Now()
	return s.cleanup()
}
func (s *Spool) preserveTail(f *os.File, id uint64, off, size int64) error {
	if size-off > maxFrameBytes+frameHeaderSize {
		return ErrSpoolCorrupt
	}
	dir := filepath.Join(s.dir, "quarantine")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b := make([]byte, size-off)
	if _, err := f.ReadAt(b, off); err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	name := filepath.Join(dir, fmt.Sprintf("tail-%020d-%020d-%x.bin", id, off, sum))
	if err := writeQueueFile(name, b); err != nil {
		return err
	}
	log.Printf("[agent] incomplete queue tail preserved at %s (%d bytes); replaying complete frames", name, len(b))
	return nil
}
func (s *Spool) commitSource(p PendingEvent) {
	s.sources.Commit(p.Key, p.Offset, p.Inode, p.Mtime, p.Head, p.Generation, p.Reset)
}
func (s *Spool) EnqueueBatch(batch []PendingEvent) error {
	if len(batch) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	if s.poison != nil {
		return s.poison
	}
	if len(batch) > maxFrameEvents {
		return fmt.Errorf("queue batch exceeds %d events", maxFrameEvents)
	}
	// Only within-batch duplicate suppression; no persistent per-event index.
	seen := make(map[string]bool, len(batch))
	unique := make([]PendingEvent, 0, len(batch))
	for _, p := range batch {
		if p.Ev.ID != "" && seen[p.Ev.ID] {
			continue
		}
		seen[p.Ev.ID] = true
		unique = append(unique, p)
	}
	raw, err := encodeQueueBatch(unique)
	if err != nil {
		return err
	}
	s.compressed.Reset()
	if s.compressor == nil {
		s.compressor, err = gzip.NewWriterLevel(&s.compressed, flate.BestSpeed)
		if err != nil {
			return err
		}
	} else {
		s.compressor.Reset(&s.compressed)
	}
	if _, err = s.compressor.Write(raw); err != nil {
		return err
	}
	if err = s.compressor.Close(); err != nil {
		return err
	}
	payload := s.compressed.Bytes()
	if len(payload) > maxFrameBytes {
		return fmt.Errorf("%w: encoded batch must be split", ErrSpoolFull)
	}
	size := int64(frameHeaderSize + len(payload))
	if s.maxBytes > 0 && s.diskBytes+size > s.maxBytes {
		if err = s.checkpoint(true); err != nil {
			return err
		}
		if s.diskBytes+size > s.maxBytes {
			return ErrSpoolFull
		}
	}
	if s.nextSeq <= 0 || s.nextSeq > int64(^uint64(0)>>1)-int64(len(unique)) {
		return errors.New("queue sequence exhausted")
	}
	created := false
	limit := s.segmentBytes
	if limit <= 0 {
		limit = segmentTarget
	}
	if len(s.segments) == 0 || s.segments[len(s.segments)-1].size+size > limit {
		if s.writer != nil {
			if err = s.writer.Close(); err != nil {
				return err
			}
			s.writer = nil
		}
		f, e := os.OpenFile(s.segmentPath(uint64(s.nextSeq)), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		s.writer = f
		s.segments = append(s.segments, queueSegment{id: uint64(s.nextSeq)})
		created = true
	} else if s.writer == nil {
		s.writer, err = os.OpenFile(s.segmentPath(s.segments[len(s.segments)-1].id), os.O_RDWR, 0600)
		if err != nil {
			return err
		}
	}
	seg := &s.segments[len(s.segments)-1]
	off := seg.size
	var header [frameHeaderSize]byte
	copy(header[:4], "LQ01")
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[8:12], uint32(len(raw)))
	binary.LittleEndian.PutUint32(header[12:16], uint32(len(unique)))
	binary.LittleEndian.PutUint64(header[16:24], uint64(s.nextSeq))
	binary.LittleEndian.PutUint32(header[24:28], crc32.ChecksumIEEE(payload))
	binary.LittleEndian.PutUint32(header[28:32], crc32.ChecksumIEEE(header[:28]))
	if _, err = s.writer.WriteAt(header[:], off); err == nil {
		_, err = s.writer.WriteAt(payload, off+frameHeaderSize)
	}
	if err == nil {
		err = s.writer.Sync()
	}
	if err == nil && created {
		err = syncQueueDir(s.dir)
	}
	if err != nil {
		s.poison = fmt.Errorf("queue append failed; restart required, source offsets retained: %w", err)
		return s.poison
	}
	if s.pending == 0 {
		s.head = queuePosition{seg.id, off}
		s.cache = nil
	}
	s.nextSeq += int64(len(unique))
	seg.last = s.nextSeq - 1
	seg.size += size
	s.diskBytes += size
	s.pending += len(unique)
	s.pendingBytes += int64(len(raw) + 128*len(unique))
	for _, p := range unique {
		s.commitSource(p)
	}
	s.dirty = true
	return nil
}
func readQueueFrame(f *os.File, off, size int64) ([]SpoolRecord, int64, error) {
	if size-off < frameHeaderSize {
		return nil, off, io.ErrUnexpectedEOF
	}
	var h [frameHeaderSize]byte
	if _, err := f.ReadAt(h[:], off); err != nil {
		return nil, off, err
	}
	if string(h[:4]) != "LQ01" || binary.LittleEndian.Uint32(h[28:]) != crc32.ChecksumIEEE(h[:28]) {
		return nil, off, errors.New("invalid frame header checksum")
	}
	n := int64(binary.LittleEndian.Uint32(h[4:8]))
	rawSize := int64(binary.LittleEndian.Uint32(h[8:12]))
	count := int(binary.LittleEndian.Uint32(h[12:16]))
	seq := int64(binary.LittleEndian.Uint64(h[16:24]))
	if n <= 0 || n > maxFrameBytes || rawSize <= 0 || rawSize > maxFrameBytes || count < 1 || count > maxFrameEvents || seq <= 0 || seq > int64(^uint64(0)>>1)-int64(count) {
		return nil, off, errors.New("invalid frame bounds")
	}
	end := off + frameHeaderSize + n
	if end > size {
		return nil, off, io.ErrUnexpectedEOF
	}
	payload := make([]byte, n)
	if _, err := f.ReadAt(payload, off+frameHeaderSize); err != nil {
		return nil, off, err
	}
	if binary.LittleEndian.Uint32(h[24:28]) != crc32.ChecksumIEEE(payload) {
		return nil, off, errors.New("invalid frame payload checksum")
	}
	z, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, off, err
	}
	raw, err := io.ReadAll(io.LimitReader(z, rawSize+1))
	closeErr := z.Close()
	if err != nil {
		return nil, off, err
	}
	if closeErr != nil {
		return nil, off, closeErr
	}
	if int64(len(raw)) != rawSize {
		return nil, off, errors.New("invalid frame decoded length")
	}
	records, err := decodeQueueBatch(raw, count, seq)
	return records, end, err
}
func (s *Spool) Fetch(maxCount, maxBytes int) ([]SpoolRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, os.ErrClosed
	}
	if s.poison != nil {
		return nil, s.poison
	}
	if err := s.checkpoint(false); err != nil {
		return nil, err
	}
	if s.pending == 0 || maxCount <= 0 {
		return nil, nil
	}
	if len(s.cache) == 0 {
		var seg *queueSegment
		for i := range s.segments {
			if s.segments[i].id == s.head.segment {
				seg = &s.segments[i]
				break
			}
		}
		if seg == nil {
			return nil, ErrSpoolCorrupt
		}
		f, err := os.Open(s.segmentPath(seg.id))
		if err != nil {
			return nil, err
		}
		s.cache, s.cacheEnd, err = readQueueFrame(f, s.head.offset, seg.size)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSpoolCorrupt, err)
		}
		for len(s.cache) > 0 && s.cache[0].Seq <= s.state.Ack {
			s.cache = s.cache[1:]
		}
		if len(s.cache) == 0 {
			return nil, ErrSpoolCorrupt
		}
	}
	n := 0
	var size int64
	for n < len(s.cache) && n < maxCount {
		if n > 0 && maxBytes > 0 && size+s.cache[n].Bytes > int64(maxBytes) {
			break
		}
		size += s.cache[n].Bytes
		n++
	}
	return append([]SpoolRecord(nil), s.cache[:n]...), nil
}
func (s *Spool) Ack(records []SpoolRecord) error {
	if len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	if records[len(records)-1].Seq <= s.state.Ack {
		return nil
	}
	if len(records) > len(s.cache) {
		return errors.New("ACK is not a fetched queue prefix")
	}
	for i, r := range records {
		if r.Seq != s.cache[i].Seq || r.Pending.Ev.ID != s.cache[i].Pending.Ev.ID {
			return errors.New("ACK is not a fetched queue prefix")
		}
	}
	s.state.Ack = records[len(records)-1].Seq
	s.pending -= len(records)
	for _, rec := range records {
		s.pendingBytes -= rec.Bytes
	}
	clear(s.cache[:len(records)])
	s.cache = s.cache[len(records):]
	s.dirty = true
	if len(s.cache) == 0 {
		s.head.offset = s.cacheEnd
		for i, seg := range s.segments {
			if seg.id == s.head.segment && s.head.offset == seg.size && i+1 < len(s.segments) {
				s.head = queuePosition{s.segments[i+1].id, 0}
				break
			}
		}
	}
	return s.checkpoint(false)
}
func (s *Spool) checkpoint(force bool) error {
	if !s.dirty {
		if force {
			return s.cleanup()
		}
		return nil
	}
	if !force && time.Since(s.lastSync) < 5*time.Second {
		return nil
	}
	s.state.Last = s.nextSeq - 1
	s.state.Sources = s.sources.states
	if err := writeQueueJSON(filepath.Join(s.dir, "checkpoint.json"), s.state); err != nil {
		return err
	}
	s.dirty = false
	s.lastSync = time.Now()
	return s.cleanup()
}
func (s *Spool) cleanup() error {
	kept := make([]queueSegment, 0, len(s.segments))
	changed := false
	for i, seg := range s.segments {
		if seg.last > s.state.Ack || (i == len(s.segments)-1 && s.pending > 0) {
			kept = append(kept, seg)
			continue
		}
		if i == len(s.segments)-1 && s.writer != nil {
			err := s.writer.Close()
			s.writer = nil
			if err != nil {
				s.segments = append(kept, s.segments[i:]...)
				return err
			}
		}
		if err := os.Remove(s.segmentPath(seg.id)); err != nil && !os.IsNotExist(err) {
			s.segments = append(kept, s.segments[i:]...)
			return err
		}
		s.diskBytes -= seg.size
		changed = true
	}
	s.segments = kept
	if changed {
		return syncQueueDir(s.dir)
	}
	return nil
}

// Bytes preserves the reporting contract: pending uncompressed data, not space
// still occupied by acknowledged frames awaiting segment reclamation.
func (s *Spool) Bytes() (int64, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.pendingBytes, nil }
func (s *Spool) Count() (int, error)   { s.mu.Lock(); defer s.mu.Unlock(); return s.pending, nil }
func (s *Spool) RestoreRegistry(reg *Registry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, st := range s.sources.states {
		reg.Commit(key, st.Offset, st.Inode, st.Mtime, st.Head, st.Generation, true)
	}
	reg.Save()
	return nil
}
func (s *Spool) release() {
	if s.writer != nil {
		s.writer.Close()
		s.writer = nil
	}
	if s.lock != nil {
		syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		s.lock.Close()
		s.lock = nil
	}
}
func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	err := s.checkpoint(true)
	s.closed = true
	s.release()
	return err
}
