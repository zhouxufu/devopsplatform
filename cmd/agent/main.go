package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"sync/atomic"
	"syscall"

	"logplatform/internal/agent"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "agent.yaml", "config file")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// 轻量约束：内存软上限 50MB，GC 更积极（用少量 CPU 换稳定内存）
	debug.SetMemoryLimit(50 << 20)
	debug.SetGCPercent(30)

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	reg := agent.LoadRegistry(cfg.Registry)
	spool, err := agent.OpenSpool(cfg.Spool, cfg.SpoolMaxMB<<20)
	if err != nil {
		log.Fatalf("open local spool: %v", err)
	}
	defer spool.Close()
	if err := spool.RestoreRegistry(reg); err != nil {
		log.Fatalf("restore registry from local spool: %v", err)
	}
	var dropped atomic.Int64

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 最坏按 256KB/条约 16MB；队列满会阻塞文件读取，不再静默丢日志。
	out := make(chan agent.PendingEvent, 64)

	for _, c := range cfg.Collect {
		t, err := agent.NewTailer(c, cfg.Host, reg, out)
		if err != nil {
			log.Fatalf("collector %s: %v", c.Service, err)
		}
		go t.Run(ctx)
		log.Printf("[agent] collecting service=%s paths=%v", c.Service, c.Paths)
	}
	go reg.SaveLoop(ctx)

	sender := agent.NewSender(cfg, out, reg, spool, &dropped)
	if cfg.MonitorURL != "" {
		go agent.NewMachineCollector(cfg.MonitorURL, cfg.Token, cfg.Host, cfg.Environment).Run(ctx)
		log.Printf("[agent] machine metrics environment=%s -> %s (15s)", cfg.Environment, cfg.MonitorURL)
	}
	log.Printf("[agent] started id=%s host=%s server=%s spool=%s max=%dMB", cfg.AgentID, cfg.Host, cfg.ServerURL, cfg.Spool, cfg.SpoolMaxMB)
	sender.Run(ctx) // 阻塞至退出（退出前 flush）
	log.Printf("[agent] stopped")
}
