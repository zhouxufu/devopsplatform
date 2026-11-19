package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTailerMergesJavaStackAndFlushesFinalEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	stack := strings.Join([]string{
		"2026-09-16 10:24:37.025 [Dubbo-framework-metadata-retry-thread-1] ERROR Failed to get metadata",
		"org.apache.dubbo.rpc.RpcException: Failed to invoke remote method: getMetadataInfo",
		"\tat org.apache.dubbo.rpc.protocol.dubbo.DubboInvoker.doInvoke(DubboInvoker.java:158)",
		"Caused by: java.net.ConnectException: Connection refused",
		"\tat java.net.Socket.connect(Socket.java:633)",
		"\tSuppressed: java.io.IOException: closed",
		"\t... 12 more",
	}, "\n")
	last := "2026-09-16 10:24:38.100 [main] INFO recovered"
	content := stack + "\n" + last + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan PendingEvent, 16)
	// Same ordinary regex emitted by the Java preset; no new Agent protocol needed.
	cfg := CollectCfg{Service: "orders", Environment: "test", Paths: []string{path}, Multiline: `^\s*\[?(\d{4}[-/]\d{2}[-/]\d{2}[ T]|\d{2}:\d{2}:\d{2})`}
	tailer, err := NewTailer(cfg, "node-a", LoadRegistry(filepath.Join(dir, "registry.json")), out)
	if err != nil {
		t.Fatal(err)
	}
	go tailer.tailFile(ctx, path)
	first := receivePending(t, out, 3*time.Second)
	if first.Ev.Message != stack || first.Offset != int64(len(stack)+1) {
		t.Fatalf("stack must be one event with the exact checkpoint: %+v", first)
	}
	final := receivePending(t, out, 5*time.Second)
	if final.Ev.Message != last || final.Offset != int64(len(content)) {
		t.Fatalf("final event must flush at idle EOF: %+v", final)
	}
	if len(out) != 0 {
		t.Fatal("stack was emitted as additional individual events")
	}
}

func receivePending(t *testing.T, ch <-chan PendingEvent, timeout time.Duration) PendingEvent {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(timeout):
		t.Fatal("timed out waiting for tailed event")
		return PendingEvent{}
	}
}

func TestTailerDrainsRenamedFDBeforeOpeningReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	oldWriter, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer oldWriter.Close()
	if _, err := oldWriter.WriteString("first\n"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan PendingEvent, 8)
	tailer, err := NewTailer(CollectCfg{Service: "api", Environment: "test", Paths: []string{path}}, "node-a", LoadRegistry(filepath.Join(dir, "registry.json")), out)
	if err != nil {
		t.Fatal(err)
	}
	go tailer.tailFile(ctx, path)
	if got := receivePending(t, out, 3*time.Second); got.Ev.Message != "first" {
		t.Fatalf("first event = %q", got.Ev.Message)
	}

	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 模拟应用在 rename 后仍通过旧 FD 写入最后一行。
	if _, err := oldWriter.WriteString("old-tail\n"); err != nil {
		t.Fatal(err)
	}
	if got := receivePending(t, out, 4*time.Second); got.Ev.Message != "old-tail" {
		t.Fatalf("rotated tail event = %q", got.Ev.Message)
	}
	if got := receivePending(t, out, 5*time.Second); got.Ev.Message != "new-file" {
		t.Fatalf("replacement event = %q", got.Ev.Message)
	}
}

func TestTailerColoredJavaUsesRawOffsetsAndIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "colored.log")
	first := "\x1b[32m2026-11-18 08:21:59 943\x1b[0;39m  \x1b[31mERROR\x1b[0m [main] failed\njava.io.IOException: failed\n\tat Example.run(Example.java:7)"
	second := "\x1b[32m2026-11-18 08:22:00 001\x1b[0m \x1b[34mINFO\x1b[0m [main] recovered"
	content := first + "\n" + second + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan PendingEvent, 8)
	cfg := CollectCfg{Service: "orders", Environment: "test", Paths: []string{path}, Multiline: `^\s*\[?(\d{4}[-/]\d{2}[-/]\d{2}[ T]|\d{2}:\d{2}:\d{2})`}
	tailer, err := NewTailer(cfg, "node-a", LoadRegistry(filepath.Join(dir, "registry.json")), out)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); tailer.tailFile(ctx, path) }()
	a := receivePending(t, out, 3*time.Second)
	b := receivePending(t, out, 5*time.Second)
	if a.Ev.Message != first || a.Offset != int64(len(first)+1) || b.Ev.Message != second || b.Offset != int64(len(content)) {
		t.Fatalf("raw boundaries changed: %+v %+v", a, b)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := eventID("node-a", "test", "orders", path, a.Generation, stDevice(stat), stInode(stat), 0, a.Offset, first)
	if a.Ev.ID != want {
		t.Fatal("colored input identity changed", a.Ev.ID, want)
	}
	if len(out) != 0 {
		t.Fatal("stack split or consecutive headers merged")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("tailer did not exit")
	}
}
