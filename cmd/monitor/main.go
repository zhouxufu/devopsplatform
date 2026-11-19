package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"logplatform/internal/monitor"
)

func main() {
	addr := flag.String("addr", ":9802", "data-plane listen (machine metrics now, OTLP later)")
	adminAddr := flag.String("admin-addr", "127.0.0.1:19802", "admin-plane listen (gateway)")
	data := flag.String("data", "./monitor-data", "data directory")
	dbDriver := flag.String("db-driver", envOr("DEVOPSPLATFORM_MONITOR_DB_DRIVER", "sqlite"), "monitor database driver: sqlite or mysql")
	dbDSN := flag.String("db-dsn", os.Getenv("DEVOPSPLATFORM_MONITOR_DB_DSN"), "monitor database DSN (prefer DEVOPSPLATFORM_MONITOR_DB_DSN for secrets)")
	bus := flag.String("bus", "http://127.0.0.1:9800", "platform base url (event bus)")
	deploy := flag.String("deploy", envOr("DEVOPSPLATFORM_DEPLOY_INTERNAL", "http://127.0.0.1:19803"), "trusted deployment backend for managed Arthas instances")
	token := flag.String("token", os.Getenv("DEVOPSPLATFORM_DATA_TOKEN"), "data-plane shared token (or env DEVOPSPLATFORM_DATA_TOKEN)")
	spanAttrs := flag.Bool("span-attrs", true, "store span attributes & exception events (512B value truncation)")
	flag.Parse()

	st, err := monitor.OpenDatabase(*data, monitor.DatabaseConfig{Driver: *dbDriver, DSN: *dbDSN})
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()
	st.ConfigureArthasDeploy(*deploy)

	traceStore, err := monitor.NewTraceStore(st.DB(), *spanAttrs)
	if err != nil {
		log.Fatalf("open trace store: %v", err)
	}

	monitor.SubscribeLoop(*bus, "http://"+*adminAddr) // 周期登记总线订阅（logs.metrics.*）

	mux := monitor.Routes(st, traceStore)
	go func() {
		log.Printf("[monitor] admin plane on %s", *adminAddr)
		if err := http.ListenAndServe(*adminAddr, mux); err != nil {
			log.Fatalf("admin listen: %v", err)
		}
	}()
	go func() { // 数据面：机器指标 + OTLP
		log.Printf("[monitor] data plane on %s (machine + otlp)", *addr)
		if err := http.ListenAndServe(*addr, monitor.DataRoutes(*token, st, traceStore, *bus)); err != nil {
			log.Fatalf("data listen: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[monitor] shutting down")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
