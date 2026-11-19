package extensions

import (
	"crypto/subtle"
	"encoding/json"
	"logplatform/pkg/extensionclient"
	p "logplatform/pkg/extensionprotocol"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// StartBroker exposes only host-authorized capabilities to a trusted backend.
// Its token never enters browsers, commands, persisted build snapshots or logs.
func (m *Manager) StartBroker(dir string, capabilities []string) (string, func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	token := random()
	allowed := map[string]bool{}
	for _, c := range capabilities {
		allowed[c] = true
	}
	config, err := filepath.Abs(filepath.Join(dir, "client.json"))
	if err != nil {
		ln.Close()
		return "", nil, err
	}
	if err := atomicFile(config, extensionclient.Config{URL: "http://" + ln.Addr().String(), Token: token}); err != nil {
		ln.Close()
		return "", nil, err
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/invoke" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		var in struct {
			ID      string    `json:"id"`
			Request p.Request `json:"request"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil || !allowed[in.Request.Capability] {
			http.Error(w, "unsupported", 403)
			return
		}
		in.Request.Actor = p.Actor{User: "platform-build", Role: "admin"}
		out, err := m.Invoke(r.Context(), in.ID, in.Request, false)
		if err != nil {
			e, ok := err.(*p.Error)
			if !ok {
				e = fail("unavailable", "插件暂不可用")
			}
			out = &p.Response{Error: e}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})}
	go srv.Serve(ln)
	return config, func() { srv.Close(); os.Remove(config) }, nil
}
