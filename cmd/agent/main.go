package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"syscall"

	"logplatform/internal/agent"
	"logplatform/internal/agent/deployment"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "agent.yaml", "config file")
	showIdentity := flag.Bool("identity", false, "print persisted installation identity without starting collectors")
	showVersion := flag.Bool("version", false, "print version")
	workerDir := flag.String("deployment-worker", "", "internal: run one persisted deployment worker")
	mergeConfig := flag.String("merge-collect-config", "", "internal: preserve deployment policy in a proposed log configuration")
	mergeUser := flag.String("deployment-run-user", "", "internal: expected installed Agent user")
	prepareDeployment := flag.String("prepare-deployment-config", "", "internal: prepare a private enrollment candidate")
	checkDeployment := flag.Bool("check-deployment-worker", false, "internal: verify independent deployment worker")
	workerCheck := flag.String("deployment-worker-check", "", "internal: fixed isolated worker probe")
	flag.Parse()
	if *prepareDeployment != "" {
		if e := agent.PrepareDeploymentConfig(*configPath, *prepareDeployment); e != nil {
			log.Fatal(e)
		}
		return
	}
	if *checkDeployment {
		if e := agent.CheckDeploymentEnrollment(*configPath); e != nil {
			log.Fatal(e)
		}
		return
	}
	if *workerCheck != "" {
		if e := deployment.RunWorkerCheck(*workerCheck); e != nil {
			log.Fatal(e)
		}
		return
	}
	if *mergeConfig != "" {
		if err := agent.MergeCollectionConfig(*configPath, *mergeConfig, *mergeUser); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// Keep the memory budget and a moderate heap growth target. Reused encoder
	// buffers avoid forcing a collection for every small upload.
	debug.SetMemoryLimit(50 << 20)
	debug.SetGCPercent(50)
	if *workerDir != "" {
		if err := deployment.RunWorker(*workerDir); err != nil {
			log.Fatal(err)
		}
		return
	}

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *showIdentity {
		id, err := agent.ReadIdentity(cfg.Spool)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(id)
		return
	}
	reg := agent.LoadRegistry(cfg.Registry)
	spool, err := agent.OpenSpool(cfg.Spool, cfg.SpoolMaxMB<<20)
	if err != nil {
		log.Fatalf("open local spool: %v", err)
	}
	defer spool.Close()
	if err := spool.BindIdentity(cfg); err != nil {
		log.Fatalf("bind persistent agent identity: %v", err)
	}
	if err := spool.RestoreRegistry(reg); err != nil {
		log.Fatalf("restore registry from local spool: %v", err)
	}
	var dropped atomic.Int64

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var modules sync.WaitGroup
	if cfg.Deployment.Enabled {
		policy := cfg.Deployment
		policy.AgentID, policy.Host, policy.Environment = cfg.AgentID, cfg.Host, cfg.Environment
		manager, err := deployment.Open(policy)
		if err != nil {
			log.Fatalf("deployment module: %v", err)
		}
		defer manager.Close()
		modules.Add(1)
		go func() { defer modules.Done(); manager.Run(ctx, func(e error) { log.Printf("[deployment] %v", e) }) }()
	}

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
	if len(cfg.Collect) > 0 {
		sender.Run(ctx)
	} else {
		<-ctx.Done()
	}
	modules.Wait()
	log.Printf("[agent] stopped")
}
