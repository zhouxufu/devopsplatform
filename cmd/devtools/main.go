package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"logplatform/internal/devtools"
)

func main() {
	adminAddr := flag.String("admin-addr", "127.0.0.1:19804", "admin and MCP listen address (platform gateway)")
	data := flag.String("data", "./devtools-data", "token and audit data directory")
	logsURL := flag.String("logs", "http://127.0.0.1:19801", "logs plugin admin URL")
	monitorURL := flag.String("monitor", "http://127.0.0.1:19802", "monitor plugin admin URL")
	deployURL := flag.String("deploy", "http://127.0.0.1:19803", "deploy plugin admin URL")
	flag.Parse()

	tokens, err := devtools.OpenTokenStore(*data)
	if err != nil {
		log.Fatalf("open token store: %v", err)
	}
	tools := devtools.NewToolService(devtools.Upstreams{Logs: *logsURL, Monitor: *monitorURL, Deploy: *deployURL})
	server := &http.Server{Addr: *adminAddr, Handler: devtools.Routes(tokens, tools)}
	go func() {
		log.Printf("[devtools] MCP Streamable HTTP on %s/mcp", *adminAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[devtools] shutting down")
}
