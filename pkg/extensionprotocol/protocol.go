// Package extensionprotocol is a dependency-free wire contract for separately
// installed processes. Vendor SDKs and platform persistence do not belong here.
package extensionprotocol

import "encoding/json"

const Major = 1
const LogsSearch = "logs.search.v1"

type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}
type Manifest struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	Protocol      int               `json:"protocol"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	Entrypoint    string            `json:"entrypoint"`
	SHA256        string            `json:"sha256"`
	Capabilities  []string          `json:"capabilities"`
	Access        map[string]string `json:"access,omitempty"`
	Configuration []Field           `json:"configuration"`
}
type Description struct {
	Protocol     int      `json:"protocol"`
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}
type Actor struct {
	User string `json:"user"`
	Role string `json:"role"`
}
type Request struct {
	ExpectedVersion string          `json:"expected_version,omitempty"`
	ExpectedDigest  string          `json:"expected_digest,omitempty"`
	RequestID       string          `json:"request_id"`
	Capability      string          `json:"capability"`
	Operation       string          `json:"operation"`
	Actor           Actor           `json:"actor"`
	Input           json.RawMessage `json:"input"`
}
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string { return e.Message }

type Response struct {
	Provider *ProviderInfo   `json:"provider,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Error    *Error          `json:"error,omitempty"`
}

// ProviderInfo is supplied by the host Manager, never trusted from plugin output.
type ProviderInfo struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}
type LogSearch struct {
	Service     string   `json:"service"`
	Environment string   `json:"environment"`
	Start       int64    `json:"start"`
	End         int64    `json:"end"`
	Host        string   `json:"host"`
	Levels      []string `json:"levels"`
	Text        string   `json:"text"`
	Limit       int      `json:"limit"`
	Cursor      string   `json:"cursor"`
}
type LogHit struct {
	EventID     string `json:"event_id,omitempty"`
	Ts          int64  `json:"ts"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Host        string `json:"host"`
	Path        string `json:"path"`
	Level       string `json:"level"`
	TraceID     string `json:"trace_id,omitempty"`
	Message     string `json:"message"`
}
type LogResult struct {
	Hits       []LogHit `json:"hits"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
	Complete   bool     `json:"complete"`
	Truncated  bool     `json:"truncated"`
}
