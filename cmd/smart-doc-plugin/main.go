package main

import (
	"context"
	"logplatform/internal/smartdocplugin"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	addr, token := os.Getenv("PLUGIN_LISTEN_ADDR"), os.Getenv("PLUGIN_AUTH_TOKEN")
	if addr == "" || token == "" {
		os.Exit(1)
	}
	srv := &http.Server{Addr: addr, Handler: smartdocplugin.Handler(token), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			os.Exit(1)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
