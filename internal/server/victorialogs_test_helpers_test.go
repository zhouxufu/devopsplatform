package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var testVL struct {
	sync.Mutex
	url  string
	stop func()
	err  error
	next int
}

func TestMain(m *testing.M) {
	code := m.Run()
	if testVL.stop != nil {
		testVL.stop()
	}
	os.Exit(code)
}
func testVictoriaLogs(t *testing.T) (string, string) {
	t.Helper()
	testVL.Lock()
	defer testVL.Unlock()
	if testVL.url == "" && testVL.err == nil {
		binary := os.Getenv("VLOGS_TEST_BINARY")
		if binary == "" {
			t.Skip("actual VictoriaLogs integration requires VLOGS_TEST_BINARY; no SQLite fallback")
		}
		root, err := os.MkdirTemp("", "devops-vl-tests-")
		if err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		ln.Close()
		file, err := os.Create(filepath.Join(root, "server.log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "-httpListenAddr="+addr, "-storageDataPath="+filepath.Join(root, "logs"), "-retentionPeriod=7d", "-memory.allowedBytes=128MiB", "-inmemoryDataFlushInterval=1s", "-insert.maxLineSizeBytes=2MiB")
		cmd.Env = append(os.Environ(), "GOMAXPROCS=2", "GOMEMLIMIT=512MiB")
		cmd.Stdout = file
		cmd.Stderr = file
		if err = cmd.Start(); err != nil {
			file.Close()
			os.RemoveAll(root)
			t.Fatal(err)
		}
		testVL.stop = func() { cmd.Process.Signal(os.Interrupt); cmd.Wait(); file.Close(); os.RemoveAll(root) }
		testVL.url = "http://" + addr
		ready := false
		for i := 0; i < 100; i++ {
			r, e := http.Get(testVL.url + "/health")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					ready = true
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			b, _ := os.ReadFile(filepath.Join(root, "server.log"))
			testVL.err = fmt.Errorf("VictoriaLogs failed: %s", b)
		}
	}
	if testVL.err != nil {
		t.Fatal(testVL.err)
	}
	testVL.next++
	return testVL.url, fmt.Sprint(testVL.next)
}
func openTestLogStorage(t *testing.T) (*Storage, error) {
	t.Helper()
	u, tenant := testVictoriaLogs(t)
	o := DefaultStorageOptions()
	o.URL = u
	o.Token = ""
	o.ProjectID = tenant
	o.QueueMaxBytes = 64 << 20
	return OpenVictoriaLogs(t.TempDir(), o)
}
func awaitTestLogDelivery(t *testing.T, s *Storage) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = s.deliverOne()
		resp, err := s.request(s.ctx, "POST", "/internal/force_flush", nil, nil, "")
		if err == nil {
			resp.Body.Close()
		}
		var packed [][]byte
		rows, e := s.db.Query(`SELECT payload FROM log_batches`)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var b []byte
			rows.Scan(&b)
			packed = append(packed, b)
		}
		rows.Close()
		ready := true
		for _, b := range packed {
			events, e := unpackLogs(b)
			if e != nil {
				t.Fatal(e)
			}
			visible, e := s.verifyBatch(events)
			if e != nil || !visible {
				ready = false
				break
			}
		}
		if ready {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("queued logs not visible in actual VictoriaLogs")
}
