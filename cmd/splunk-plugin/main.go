package main

import (
	"context"
	"encoding/json"
	"logplatform/internal/splunkplugin"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	raw, err := os.ReadFile(os.Getenv("PLUGIN_CONFIG_PATH"))
	if err != nil {
		os.Exit(1)
	}
	addr, token := os.Getenv("PLUGIN_LISTEN_ADDR"), os.Getenv("PLUGIN_AUTH_TOKEN")
	if addr == "" || token == "" {
		os.Exit(1)
	}
	var config splunkplugin.Config
	err = json.Unmarshal(raw, &config)
	var adapter *splunkplugin.Adapter
	if err == nil {
		adapter, err = splunkplugin.New(config)
	}
	var handler http.Handler
	if err != nil {
		handler = splunkplugin.ConfigurationErrorHandler(token, err)
	} else {
		defer adapter.Close()
		handler = splunkplugin.Handler(adapter, token)
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
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
