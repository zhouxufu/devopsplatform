package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// MachineCollector 机器指标采集：15s 采样（CPU%/内存/磁盘/网络速率），直报 monitor 数据面。
// 开销：一次 sysctl/proc 读取，CPU 毫秒级；net 速率由相邻采样差值计算。
type MachineCollector struct {
	url         string // monitor 数据面，如 http://host:9802
	token       string
	host        string
	environment string
	client      *http.Client

	lastNet   net.IOCountersStat
	lastNetAt time.Time
}

// MachineSample 上报单元（mem/disk 单位 GB，net 单位 KB/s）
type MachineSample struct {
	AgentID     string  `json:"agent_id"`
	Environment string  `json:"environment"`
	Ts          int64   `json:"ts"`
	CPU         float64 `json:"cpu"`
	MemUsed     float64 `json:"mem_used"`
	MemTotal    float64 `json:"mem_total"`
	DiskUsed    float64 `json:"disk_used"`
	DiskTotal   float64 `json:"disk_total"`
	NetRxKBs    float64 `json:"net_rx_kbs"`
	NetTxKBs    float64 `json:"net_tx_kbs"`
}

func NewMachineCollector(url, token, host, environment string) *MachineCollector {
	return &MachineCollector{url: url, token: token, host: host, environment: environment, client: &http.Client{Timeout: 8 * time.Second}}
}

func (m *MachineCollector) Run(ctx context.Context) {
	tk := time.NewTicker(15 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if s, ok := m.sample(); ok {
				m.report(s)
			}
		}
	}
}

func (m *MachineCollector) sample() (*MachineSample, bool) {
	s := &MachineSample{AgentID: m.host, Environment: m.environment, Ts: time.Now().UnixMilli()}
	if v, err := cpu.Percent(0, false); err == nil && len(v) > 0 {
		s.CPU = round1(v[0])
	}
	if v, err := mem.VirtualMemory(); err == nil {
		s.MemUsed, s.MemTotal = round1(float64(v.Used)/1e9), round1(float64(v.Total)/1e9)
	}
	if v, err := disk.Usage("/"); err == nil {
		s.DiskUsed, s.DiskTotal = round1(float64(v.Used)/1e9), round1(float64(v.Total)/1e9)
	}
	if cs, err := net.IOCounters(false); err == nil && len(cs) > 0 {
		now := time.Now()
		if !m.lastNetAt.IsZero() {
			dt := now.Sub(m.lastNetAt).Seconds()
			if dt > 1 {
				s.NetRxKBs = round1(float64(cs[0].BytesRecv-m.lastNet.BytesRecv) / 1024 / dt)
				s.NetTxKBs = round1(float64(cs[0].BytesSent-m.lastNet.BytesSent) / 1024 / dt)
			}
		}
		m.lastNet, m.lastNetAt = cs[0], now
	}
	return s, true
}

func (m *MachineCollector) report(s *MachineSample) {
	body, _ := json.Marshal(s)
	req, err := http.NewRequest("POST", m.url+"/api/machine", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if m.token != "" {
		req.Header.Set("X-Token", m.token)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return // 机器指标 best-effort，不重试不记噪音日志
	}
	resp.Body.Close()
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
