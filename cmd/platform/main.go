package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"logplatform/internal/platform"
)

func main() {
	addr := flag.String("addr", envOr("DEVOPSPLATFORM_ADDR", ":9000"), "console listen address")
	pluginsDir := flag.String("plugins", "./plugins", "plugins directory (manifest per sub-dir)")
	dataDir := flag.String("data", "./platform-data", "platform data dir (registry.db)")
	dbDriver := flag.String("db-driver", envOr("DEVOPSPLATFORM_DB_DRIVER", "sqlite"), "catalog database driver: sqlite or mysql")
	dbDSN := flag.String("db-dsn", os.Getenv("DEVOPSPLATFORM_DB_DSN"), "catalog database DSN (prefer DEVOPSPLATFORM_DB_DSN for secrets)")
	authDefault := envOr("DEVOPSPLATFORM_AUTH", os.Getenv("LOGPLATFORM_AUTH"))
	auth := flag.String("auth", authDefault, "admin password (or DEVOPSPLATFORM_AUTH; empty = no login, intranet only)")
	flag.Parse()

	ms, err := platform.DiscoverPlugins(*pluginsDir)
	if err != nil {
		log.Fatalf("discover plugins: %v", err)
	}
	log.Printf("[platform] found %d plugin(s): %v", len(ms), names(ms))

	gw := platform.NewGateway(*auth)
	gw.SetPlugins(ms)

	os.MkdirAll(*dataDir, 0o755)
	registryDSN := *dbDSN
	if registryDSN == "" && *dbDriver == "sqlite" {
		registryDSN = filepath.Join(*dataDir, "registry.db")
	}
	if registryDSN == "" {
		log.Fatalf("open registry: DEVOPSPLATFORM_DB_DSN is required when db-driver=%s", *dbDriver)
	}
	registry, err := platform.OpenRegistryDatabase(platform.RegistryConfig{Driver: *dbDriver, DSN: registryDSN})
	if err != nil {
		log.Fatalf("open registry: %v", err)
	}
	defer registry.Close()
	if err := registry.EnsureBootstrapAdmin(*auth); err != nil {
		log.Fatalf("initialize platform admin: %v", err)
	}
	bus := platform.NewBus()

	sup := platform.NewSupervisor()
	sup.StartAll(ms) // 拉起插件进程

	srv := &http.Server{Addr: *addr, Handler: gw.Routes(registry, bus)}
	go func() {
		displayAddr := *addr
		if displayAddr != "" && displayAddr[0] == ':' {
			displayAddr = "127.0.0.1" + displayAddr
		}
		log.Printf("[platform] console on http://%s (auth=%v)", displayAddr, *auth != "")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[platform] shutting down (stopping plugins...)")
	sup.StopAll()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func names(ms []*platform.Manifest) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}
