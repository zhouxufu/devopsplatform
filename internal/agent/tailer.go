package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"logplatform/internal/types"
)

func stInode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func stDevice(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}

const (
	maxLineLen  = 256 << 10 // 单条日志上限 256KB
	maxEventLen = 256 << 10 // 多行合并后上限
)

// PendingEvent 尚未确认发送的事件，携带 offset 供发送成功后提交
type PendingEvent struct {
	Ev         types.Event
	Key        string
	Offset     int64
	Inode      uint64
	Mtime      int64
	Head       string
	Generation string
	Reset      bool // copytruncate 的首个事件强制开启新的 offset 世代
}

// Tailer 一个采集项（service + 多个 glob 路径）的文件发现与读取
type Tailer struct {
	cfg     CollectCfg
	host    string
	reg     *Registry
	out     chan<- PendingEvent
	multiRe *regexp.Regexp

	mu      sync.Mutex
	started map[string]struct{}
}

func NewTailer(cfg CollectCfg, host string, reg *Registry, out chan<- PendingEvent) (*Tailer, error) {
	t := &Tailer{cfg: cfg, host: host, reg: reg, out: out, started: map[string]struct{}{}}
	if cfg.Multiline != "" {
		re, err := regexp.Compile(cfg.Multiline)
		if err != nil {
			return nil, err
		}
		t.multiRe = re
	}
	return t, nil
}

// Run 每 10s 扫描 glob，发现新文件即启动读取协程
func (t *Tailer) Run(ctx context.Context) {
	t.scan(ctx)
	tk := time.NewTicker(10 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			t.scan(ctx)
		}
	}
}

func (t *Tailer) scan(ctx context.Context) {
	for _, pattern := range t.cfg.Paths {
		matches, _ := filepath.Glob(pattern)
		for _, p := range matches {
			t.mu.Lock()
			_, running := t.started[p]
			if !running {
				t.started[p] = struct{}{}
			}
			t.mu.Unlock()
			if !running {
				go t.tailFile(ctx, p)
			}
		}
	}
}

func (t *Tailer) forget(path string) {
	t.mu.Lock()
	delete(t.started, path)
	t.mu.Unlock()
}

const rotateDrainWait = 2 * time.Second

// tailFile 为活跃文件长期持有 FD。rename 后先把旧 FD 排空，再切换同路径的新 inode；
// copytruncate 则在同一 FD 上回到文件头。EOF 使用自适应退避，避免反复 open/stat/seek。
func (t *Tailer) tailFile(ctx context.Context, path string) {
	defer t.forget(path)
	key := t.cfg.Service + "|" + path
	merger := newMerger(t.multiRe)
	var f *os.File
	var r *bufio.Reader
	var inode, device uint64
	var head string
	var generation string
	var offset int64
	var mtime int64

	open := func(resume bool) error {
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		nf, err := os.Open(path)
		if err != nil {
			return err
		}
		inode, device = stInode(st), stDevice(st)
		head, mtime = readHeadSig(path), st.ModTime().Unix()
		offset = 0
		if resume {
			offset, generation = t.reg.ResolveFile(key, inode, mtime, head)
			if offset > st.Size() {
				offset = 0
			}
		} else {
			generation = newGeneration()
		}
		if _, err := nf.Seek(offset, io.SeekStart); err != nil {
			nf.Close()
			return err
		}
		f, r = nf, bufio.NewReaderSize(nf, 64<<10)
		return nil
	}
	if err := open(true); err != nil {
		return
	}
	defer func() {
		if f != nil {
			f.Close()
		}
	}()

	backoff := 250 * time.Millisecond
	var rotatedAt time.Time
	resetOffset := false

	for {
		lineStart := offset
		line, consumed, readErr := readLine(r, maxLineLen)
		if consumed > 0 {
			offset += consumed
			if st, err := f.Stat(); err == nil {
				mtime = st.ModTime().Unix()
			}
			if len(head) < 172 { // base64(128 bytes) 长度为 172；文件初建时补齐稳定签名
				head = readHeadSig(path)
			}
			now := time.Now()
			if ev, ok := merger.add(line, now, lineStart, offset); ok {
				if !t.emit(ctx, ev, now, path, key, device, inode, mtime, head, generation, resetOffset) {
					return
				}
				resetOffset = false
			}
			backoff = 250 * time.Millisecond
			rotatedAt = time.Time{}
			if readErr == nil {
				continue
			}
		}

		if ev, ok := merger.flushIfStale(2 * time.Second); ok {
			if !t.emit(ctx, ev, time.Now(), path, key, device, inode, mtime, head, generation, resetOffset) {
				return
			}
			resetOffset = false
		}

		pathSt, pathErr := os.Stat(path)
		if pathErr != nil || stInode(pathSt) != inode || stDevice(pathSt) != device {
			// 路径已经指向新文件（或暂时消失），旧 FD 仍然有效；给旧 writer 留出尾部写入时间。
			if rotatedAt.IsZero() {
				rotatedAt = time.Now()
			}
			if time.Since(rotatedAt) >= rotateDrainWait {
				if ev, ok := merger.flush(); ok && !t.emit(ctx, ev, time.Now(), path, key, device, inode, mtime, head, generation, resetOffset) {
					return
				}
				resetOffset = false
				f.Close()
				f = nil
				if pathErr != nil || open(false) != nil {
					return
				}
				merger.reset()
				rotatedAt = time.Time{}
				backoff = 250 * time.Millisecond
				continue
			}
		} else {
			rotatedAt = time.Time{}
			fdSt, statErr := f.Stat()
			newHead := head
			if len(head) >= 172 {
				newHead = readHeadSig(path)
			}
			// size 回退或稳定文件头变化都表明发生了 copytruncate。
			if statErr == nil && (fdSt.Size() < offset || (len(head) >= 172 && newHead != "" && newHead != head)) {
				if ev, ok := merger.flush(); ok && !t.emit(ctx, ev, time.Now(), path, key, device, inode, mtime, head, generation, resetOffset) {
					return
				}
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return
				}
				r.Reset(f)
				offset, head = 0, newHead
				generation = newGeneration()
				resetOffset = true
				backoff = 250 * time.Millisecond
				continue
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 2*time.Second {
			backoff *= 2
			if backoff > 2*time.Second {
				backoff = 2 * time.Second
			}
		}
	}
}

func (t *Tailer) emit(ctx context.Context, merged mergedEvent, ts time.Time, path, key string, device, inode uint64, mtime int64, head, generation string, reset bool) bool {
	ev := types.Event{
		Ts:            ts.UnixMilli(),
		ApplicationID: t.cfg.ApplicationID,
		Service:       t.cfg.Service,
		Environment:   t.cfg.Environment,
		Host:          t.host,
		Path:          path,
		Message:       merged.Message,
	}
	ev.ID = eventID(t.host, t.cfg.Environment, t.cfg.Service, path, generation, device, inode, merged.Start, merged.End, merged.Message)
	select {
	case t.out <- PendingEvent{Ev: ev, Key: key, Offset: merged.End, Inode: inode, Mtime: mtime, Head: head, Generation: generation, Reset: reset}:
		return true
	case <-ctx.Done():
		return false
	}
}

func eventID(host, environment, service, path, generation string, device, inode uint64, start, end int64, message string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00", host, environment, service, path, generation, device, inode, start, end)
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// ---------- 行读取（超长截断）与多行合并 ----------

// readLine 读一行（含换行符计入 consumed），超过 max 截断
func readLine(r *bufio.Reader, max int) (line string, consumed int64, err error) {
	var buf []byte
	truncated := false
	for {
		chunk, e := r.ReadSlice('\n')
		consumed += int64(len(chunk))
		if len(buf)+len(chunk) > max {
			room := max - len(buf)
			if room > 0 {
				buf = append(buf, chunk[:room]...)
			}
			truncated = true
		} else {
			buf = append(buf, chunk...)
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		if e != nil {
			err = e
		}
		break
	}
	line = strings.TrimRight(string(buf), "\r\n")
	if truncated {
		line += " ...[truncated]"
	}
	return
}

// merger 多行合并：行首正则匹配 = 新事件开始；不匹配的行（堆栈等）并入上一条
type merger struct {
	re       *regexp.Regexp
	buf      strings.Builder
	last     time.Time
	startOff int64
	endOff   int64
	finished bool
}

type mergedEvent struct {
	Message string
	Start   int64
	End     int64
}

func newMerger(re *regexp.Regexp) *merger { return &merger{re: re} }

func (m *merger) reset() {
	m.buf.Reset()
	m.finished = true
}

// add 返回完整事件及其精确起止 offset。新事件的首行不会推进上一事件的断点。
func (m *merger) add(line string, now time.Time, lineStart, lineEnd int64) (mergedEvent, bool) {
	if m.re == nil { // 单行模式
		return mergedEvent{Message: line, Start: lineStart, End: lineEnd}, true
	}
	newStart := m.re.MatchString(line)
	if newStart && m.buf.Len() > 0 {
		out := mergedEvent{Message: m.buf.String(), Start: m.startOff, End: m.endOff}
		m.buf.Reset()
		m.write(line, now, lineStart, lineEnd)
		return out, true
	}
	m.write(line, now, lineStart, lineEnd)
	return mergedEvent{}, false
}

func (m *merger) write(line string, now time.Time, lineStart, lineEnd int64) {
	if m.buf.Len() == 0 {
		m.startOff = lineStart
		m.finished = false
	}
	m.last = now
	m.endOff = lineEnd
	if m.buf.Len() < maxEventLen {
		if m.buf.Len() > 0 {
			m.buf.WriteByte('\n')
		}
		m.buf.WriteString(line)
	}
}

func (m *merger) flushIfStale(d time.Duration) (mergedEvent, bool) {
	if m.buf.Len() == 0 || m.finished {
		return mergedEvent{}, false
	}
	if time.Since(m.last) < d {
		return mergedEvent{}, false
	}
	return m.flush()
}

func (m *merger) flush() (mergedEvent, bool) {
	if m.buf.Len() == 0 || m.finished {
		return mergedEvent{}, false
	}
	out := mergedEvent{Message: m.buf.String(), Start: m.startOff, End: m.endOff}
	m.buf.Reset()
	m.finished = true
	return out, true
}
