package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"logplatform/internal/deploy"
)

func main() {
	adminAddr := flag.String("admin-addr", "127.0.0.1:19803", "admin-plane listen (gateway)")
	data := flag.String("data", "./deploy-data", "data directory")
	dbDriver := flag.String("db-driver", envOr("DEVOPSPLATFORM_DEPLOY_DB_DRIVER", "sqlite"), "deployment metadata database driver: sqlite or mysql")
	dbDSN := flag.String("db-dsn", os.Getenv("DEVOPSPLATFORM_DEPLOY_DB_DSN"), "deployment metadata database DSN (prefer DEVOPSPLATFORM_DEPLOY_DB_DSN for secrets)")
	bus := flag.String("bus", "http://127.0.0.1:9800", "platform event bus（发布事件标注 monitor 图表）")
	flag.Parse()

	st, err := deploy.OpenDatabase(*data, deploy.DatabaseConfig{Driver: *dbDriver, DSN: *dbDSN})
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	hub := deploy.NewLogHub()
	builder := deploy.NewBuilder(st, *data, hub)
	// 内置步骤目录按可执行文件位置推断（bin/deploy → ../steps），避免 cwd 依赖
	builtinSteps := "steps"
	if exe, err := os.Executable(); err == nil {
		builtinSteps = filepath.Join(filepath.Dir(filepath.Dir(exe)), "steps")
	}
	log.Printf("[deploy] 内置步骤: %s（回填 %d 个服务）", builtinSteps, st.BackfillSteps())
	stepLib := deploy.NewStepLib(builtinSteps, filepath.Join(*data, "steps-user")) // 用户目录覆盖内置

	go func() {
		log.Printf("[deploy] admin plane on %s (build + deploy)", *adminAddr)
		if err := http.ListenAndServe(*adminAddr, deploy.Routes(st, builder, hub, *bus, stepLib)); err != nil {
			log.Fatalf("listen: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[deploy] shutting down")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
