package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileState 断点：inode+offset；mtime 与文件头签名用于 inode 重用防护
// （文件删除后新文件可能复用 inode，签名不符则从头读，宁可重复不可丢失）
type FileState struct {
	Inode      uint64 `json:"inode"`
	Offset     int64  `json:"offset"`
	Mtime      int64  `json:"mtime"`
	Head       string `json:"head"`
	Generation string `json:"generation,omitempty"`
}

type Registry struct {
	mu     sync.Mutex
	path   string
	states map[string]*FileState
	dirty  bool
}

func LoadRegistry(path string) *Registry {
	r := &Registry{path: path, states: map[string]*FileState{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &r.states)
	}
	return r
}

// SaveLoop 每 5s 合并持久化；崩溃窗口由本地 spool 的 RestoreRegistry 修复。
func (r *Registry) SaveLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.Save()
			return
		case <-t.C:
			r.Save()
		}
	}
}

func (r *Registry) Save() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dirty {
		return
	}
	b, _ := json.Marshal(r.states)
	_ = os.MkdirAll(filepath.Dir(r.path), 0o755)
	tmp := r.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, r.path)
		r.dirty = false
	}
}

// Resolve 恢复 offset：inode/mtime/签名全部吻合才续传
func (r *Registry) Resolve(key string, inode uint64, mtime int64, head string) int64 {
	offset, _ := r.ResolveFile(key, inode, mtime, head)
	return offset
}

// ResolveFile 同时返回稳定的文件世代 ID。世代随 rename/copytruncate 改变，
// 同一文件重启后保持不变，供 event_id 在重放时保持稳定。
func (r *Registry) ResolveFile(key string, inode uint64, mtime int64, head string) (int64, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[key]
	if s != nil && s.Inode == inode && s.Mtime <= mtime && compatibleHead(s.Head, head) {
		if s.Generation == "" { // 兼容升级前 registry
			s.Generation = newGeneration()
			r.dirty = true
		}
		return s.Offset, s.Generation
	}
	return 0, newGeneration()
}

func newGeneration() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// compatibleHead 兼容文件刚创建、头 128 字节尚未写满时保存的短签名。
// 同 inode 且旧头是新头前缀可安全续读；完整文件头不一致则视为 inode 重用。
func compatibleHead(oldHead, newHead string) bool {
	oldRaw, oldErr := base64.StdEncoding.DecodeString(oldHead)
	newRaw, newErr := base64.StdEncoding.DecodeString(newHead)
	if oldErr != nil || newErr != nil {
		return oldHead == newHead
	}
	if len(oldRaw) > len(newRaw) {
		return false
	}
	for i := range oldRaw {
		if oldRaw[i] != newRaw[i] {
			return false
		}
	}
	return true
}

// Commit 在事件可靠进入本地 spool 后提交（同一文件世代 offset 只增不减）。
func (r *Registry) Commit(key string, offset int64, inode uint64, mtime int64, head, generation string, reset bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.states[key]
	// copytruncate 保留 inode 但更换文件头，此时必须开始新的 offset 世代。
	// 普通新文件从短头逐渐写满时 compatibleHead 仍为 true，不会误重置。
	if reset || s == nil || s.Inode != inode || !compatibleHead(s.Head, head) || (generation != "" && s.Generation != generation) {
		s = &FileState{Inode: inode, Offset: 0, Generation: generation}
		r.states[key] = s
	}
	if offset > s.Offset {
		s.Offset = offset
	}
	s.Mtime, s.Head = mtime, head
	if generation != "" {
		s.Generation = generation
	}
	r.dirty = true
}

// readHeadSig 文件头 128 字节签名（base64）
func readHeadSig(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b := make([]byte, 128)
	n, _ := f.Read(b)
	return base64.StdEncoding.EncodeToString(b[:n])
}
