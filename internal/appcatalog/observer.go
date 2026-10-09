package appcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Application struct {
	ApplicationID string   `json:"application_id"`
	Name          string   `json:"name"`
	Status        string   `json:"status"`
	Sources       []string `json:"sources"`
}

// Observer resolves source-specific service names through the platform catalog.
// Results are cached because log ingestion is a hot path; source observations
// are idempotent in the catalog.
type Observer struct {
	baseURL string
	client  *http.Client
	mu      sync.RWMutex
	cache   map[string]string
	queue   chan observation
}

type observation struct{ name, source, applicationID string }

func NewObserver(baseURL string) *Observer {
	o := &Observer{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), client: &http.Client{Timeout: 3 * time.Second}, cache: map[string]string{}, queue: make(chan observation, 256)}
	if o.baseURL != "" {
		go func() {
			for item := range o.queue {
				_, _ = o.Resolve(item.name, item.source, item.applicationID)
			}
		}()
	}
	return o
}

// Submit records a best-effort observation without delaying a telemetry request.
// A full queue is deliberately dropped; the next export will retry it.
func (o *Observer) Submit(name, source, applicationID string) {
	if o == nil || o.baseURL == "" || strings.TrimSpace(name) == "" || name == "unknown" {
		return
	}
	select {
	case o.queue <- observation{name: name, source: source, applicationID: applicationID}:
	default:
	}
}

func (o *Observer) Resolve(name, source, requestedID string) (string, error) {
	return o.ResolveWithSession(name, source, requestedID, "")
}

// Interactive resource onboarding forwards the current console session only
// to the configured platform catalog, not to resource connection endpoints.
func (o *Observer) ResolveWithSession(name, source, requestedID, cookie string) (string, error) {
	name, source = strings.TrimSpace(name), strings.TrimSpace(source)
	if name == "" || source == "" {
		return requestedID, nil
	}
	key := source + "\x00" + name
	o.mu.RLock()
	if id := o.cache[key]; id != "" {
		o.mu.RUnlock()
		return id, nil
	}
	o.mu.RUnlock()
	if o.baseURL == "" {
		return requestedID, nil
	}
	raw, _ := json.Marshal(map[string]string{"name": name, "source": source, "application_id": requestedID})
	req, err := http.NewRequest(http.MethodPost, o.baseURL+"/api/registry/applications/observe", bytes.NewReader(raw))
	if err != nil {
		return requestedID, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return requestedID, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return requestedID, fmt.Errorf("应用目录返回 %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var app Application
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&app); err != nil {
		return requestedID, err
	}
	if app.ApplicationID == "" {
		return requestedID, fmt.Errorf("应用目录未返回 application_id")
	}
	o.mu.Lock()
	o.cache[key] = app.ApplicationID
	o.mu.Unlock()
	return app.ApplicationID, nil
}

// Detach removes an active source from the catalog while retaining the stable
// application identity for historical logs and future re-adoption.
func (o *Observer) Detach(name, source, applicationID string) error {
	if o == nil || o.baseURL == "" {
		return nil
	}
	name, source = strings.TrimSpace(name), strings.TrimSpace(source)
	if name == "" || source == "" {
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"name": name, "source": source, "application_id": applicationID})
	req, err := http.NewRequest(http.MethodDelete, o.baseURL+"/api/registry/applications/source", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("应用目录返回 %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	o.mu.Lock()
	delete(o.cache, source+"\x00"+name)
	o.mu.Unlock()
	return nil
}
