package platform

import (
	"context"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Supervisor 插件进程守护：拉起、崩溃后指数退避重启、优雅停止
type Supervisor struct {
	mu         sync.Mutex
	procs      map[string]*pluginProc
	stopped    bool
	backendEnv map[string][]string
}

type pluginProc struct {
	m        *Manifest
	cancel   context.CancelFunc
	lastStar time.Time
}

func NewSupervisor() *Supervisor { return &Supervisor{procs: map[string]*pluginProc{}} }

// StartAll 拉起全部插件
func (s *Supervisor) StartAll(ms []*Manifest) {
	for _, m := range ms {
		if m.Backend != "" {
			continue
		}
		s.Start(m)
	}
}

func (s *Supervisor) Start(m *Manifest) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if _, ok := s.procs[m.Name]; ok {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.procs[m.Name] = &pluginProc{m: m, cancel: cancel, lastStar: time.Now()}
	s.mu.Unlock()
	go s.run(ctx, m)
	log.Printf("[platform] plugin %s started (%s -> %s)", m.Name, m.Binary, m.AdminAddr)
}

// run 守护循环：退出即按退避重启
func (s *Supervisor) run(ctx context.Context, m *Manifest) {
	backoff := time.Second
	for {
		started := time.Now()
		cmd := exec.CommandContext(ctx, m.Binary, m.Args...)
		cmd.Dir = m.Dir
		s.mu.Lock()
		extraEnv := append([]string{}, s.backendEnv[m.Name]...)
		s.mu.Unlock()
		cmd.Env = append(os.Environ(), extraEnv...)
		cmd.Stdout = logWriter{tag: m.Name}
		cmd.Stderr = logWriter{tag: m.Name}
		err := cmd.Run()
		select {
		case <-ctx.Done():
			return // 主动停止
		default:
		}
		if time.Since(started) > 5*time.Minute { // 上次运行够久视为健康，重置退避
			backoff = time.Second
		}
		log.Printf("[platform] plugin %s exited: %v, restart in %s", m.Name, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
	}
}

// StopAll 优雅停止全部插件
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	s.stopped = true
	for _, p := range s.procs {
		p.cancel()
	}
	s.mu.Unlock()
}

type logWriter struct{ tag string }

func (w logWriter) Write(b []byte) (int, error) {
	log.Printf("[%s] %s", w.tag, string(b))
	return len(b), nil
}

// SetBackendEnvironment is configured before StartAll; values are never exposed in manifests.
func (s *Supervisor) SetBackendEnvironment(name string, env []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backendEnv == nil {
		s.backendEnv = map[string][]string{}
	}
	s.backendEnv[name] = append([]string{}, env...)
}
