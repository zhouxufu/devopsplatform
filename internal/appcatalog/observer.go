package appcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Application struct {
	ApplicationID string   `json:"application_id"`
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	Identifier    string   `json:"identifier"`
	Status        string   `json:"status"`
	Sources       []string `json:"sources"`
}

// Presentation is deliberately not cached with ingestion identities. A rename
// must become visible without invalidating or replacing any source identity.
func (o *Observer) Presentations(cookie string) ([]Application, error) {
	if o.baseURL == "" {
		return nil, nil
	}
	var apps []Application
	err := o.presentationRequest(http.MethodGet, "/api/registry/applications?include_archived=true", nil, cookie, &apps)
	return apps, err
}

func (o *Observer) InitializePresentation(name, id, label, cookie string) (*Application, error) {
	if o.baseURL == "" {
		return nil, nil
	}
	var app Application
	err := o.presentationRequest(http.MethodPost, "/api/registry/applications/observe", map[string]string{"name": name, "source": "deploy", "application_id": id, "display_name": label}, cookie, &app)
	return &app, err
}

func (o *Observer) RenamePresentation(id, label, expected, cookie string) error {
	if o.baseURL == "" {
		return nil
	}
	return o.presentationRequest(http.MethodPut, "/api/registry/applications/"+url.PathEscape(id)+"/display-name", map[string]string{"display_name": label, "expected_display_name": expected}, cookie, nil)
}

func (o *Observer) presentationRequest(method, path string, body any, cookie string, result any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(method, o.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("应用目录请求结果未确认，请刷新核对后重试: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("应用目录返回 %s，请刷新核对后重试", resp.Status)
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(result)
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
	return o.DetachWithSession(name, source, applicationID, "")
}

// Interactive deletion must use the same administrator session as onboarding.
func (o *Observer) DetachWithSession(name, source, applicationID, cookie string) error {
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
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
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
