package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"logplatform/internal/server"
)

func main() {
	addr := flag.String("addr", ":9801", "data-plane listen address (agent ingest, token protected)")
	adminAddr := flag.String("admin-addr", "", "admin-plane listen address (127.0.0.1 for gateway; empty = single mode, addr serves all)")
	data := flag.String("data", "./data", "data directory")
	tokenDefault := os.Getenv("DEVOPSPLATFORM_DATA_TOKEN")
	if tokenDefault == "" {
		tokenDefault = os.Getenv("LOGQ_TOKEN")
	}
	token := flag.String("token", tokenDefault, "ingest shared token (or env DEVOPSPLATFORM_DATA_TOKEN)")
	retentionDefault := 7
	if configured := os.Getenv("DEVOPSPLATFORM_LOG_RETENTION_DAYS"); configured != "" {
		days, err := strconv.Atoi(configured)
		if err != nil || days <= 0 {
			log.Fatal("DEVOPSPLATFORM_LOG_RETENTION_DAYS must be a positive integer")
		}
		retentionDefault = days
	}
	retention := flag.Int("retention-days", retentionDefault, "VictoriaLogs retention in days (must match backend)")
	diskGB := flag.Float64("disk-gb", 2, "maximum local delivery queue disk use in GiB")
	rules := flag.String("rules", "", "trace rules json (service+regex, hot reload)")
	bus := flag.String("bus", "", "platform event bus base url (e.g. http://127.0.0.1:9800) for derived metrics")
	vlURL := flag.String("victorialogs-url", server.DefaultStorageOptions().URL, "VictoriaLogs base URL")
	replay := flag.Duration("log-replay-window", time.Hour, "retained compressed delivery replay window")
	retry := flag.Duration("log-retry-window", 48*time.Hour, "deduplication receipt window")
	flag.Parse()

	server.LoadTraceRules(*rules)

	st, err := server.OpenVictoriaLogs(*data, func() server.StorageOptions {
		o := server.DefaultStorageOptions()
		o.URL = *vlURL
		o.RetentionDays = *retention
		o.QueueMaxBytes = int64(*diskGB * 1024 * 1024 * 1024)
		o.ReplayWindow = *replay
		o.RetryWindow = *retry
		return o
	}())
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer st.Close()

	api := server.NewAPI(st, *token, *bus)
	full := api.Routes() // 管理面：全部功能
	dataPlane := http.NewServeMux()
	dataPlane.HandleFunc("POST /ingest", func(w http.ResponseWriter, r *http.Request) {
		full.ServeHTTP(w, r)
	})
	dataPlane.HandleFunc("GET /api/tail", func(w http.ResponseWriter, r *http.Request) { // CLI 直连 tail 用
		full.ServeHTTP(w, r)
	})

	run := func(a string, h http.Handler) {
		srv := &http.Server{Addr: a, Handler: h}
		go func() {
			log.Printf("[logs-plugin] listening on %s (%s)", a, map[bool]string{true: "admin", false: "data"}[a == *adminAddr])
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen %s: %v", a, err)
			}
		}()
	}
	run(*addr, dataPlane)
	if *adminAddr != "" {
		run(*adminAddr, full)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[logs-plugin] shutting down")
}
