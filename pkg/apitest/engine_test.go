package apitest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func TestLoginTokenIsolationAndAssertions(t *testing.T) {
	var login, business atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			login.Add(1)
			var v map[string]string
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v["password"] != "p\"secret" {
				t.Errorf("JSON variable escaping failed: %v", v)
			}
			fmt.Fprint(w, `{"data":{"token":"fixture-secret-token"}}`)
		case "/orders":
			business.Add(1)
			if r.Header.Get("Authorization") != "Bearer fixture-secret-token" {
				t.Error("auth token not applied")
			}
			if len(r.Header.Get("X-Request-ID")) != 32 || !strings.HasPrefix(r.Header.Get("traceparent"), "00-") {
				t.Error("correlation missing")
			}
			w.Header().Set("Set-Cookie", "session=fixture-secret-token")
			fmt.Fprint(w, `{"data":{"id":9007199254740993,"ok":true},"token":"fixture-secret-token","echo":"p\"secret"}`)
		}
	}))
	defer server.Close()
	request := Request{Method: "POST", Path: "/orders", Headers: map[string]string{"authorization": "untrusted-override"}, Body: `{"password":"{{var.PASSWORD}}"}`, Assertions: []Assertion{{Type: "status", Expected: "200"}, {Type: "json", Path: "/data/id", Expected: "9007199254740993"}, {Type: "json", Path: "/data/ok", Expected: "true"}}}
	auth := Auth{Mode: "login", Path: "/login", Body: `{"password":"{{var.PASSWORD}}"}`, TokenPointer: "/data/token", Header: "Authorization", Prefix: "Bearer "}
	result := Perform(context.Background(), fixtureClient(), server.URL, request, auth, map[string]string{"PASSWORD": "p\"secret"}, nil)
	if result.Error != "" || login.Load() != 1 || business.Load() != 1 {
		t.Fatalf("%+v", result)
	}
	for _, check := range result.Checks {
		if !check.Passed {
			t.Fatal(check)
		}
	}
	raw, _ := json.Marshal(result)
	for _, secret := range []string{"fixture-secret-token", `p\"secret`} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret leaked: %s", raw)
		}
	}
	clean := SanitizeRequest(request, map[string]string{"PASSWORD": "p\"secret"})
	if clean.Headers["authorization"] != "[REDACTED]" {
		t.Fatal(clean)
	}
}
func TestFailedAuthDoesNotSendBusinessOrRetry(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/login" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"password":"hidden"}`)
		} else {
			t.Error("business should not be sent")
		}
	}))
	defer s.Close()
	r := Perform(context.Background(), fixtureClient(), s.URL, Request{Path: "/business"}, Auth{Mode: "login", Path: "/login", Body: `{}`, TokenPointer: "/token"}, nil, nil)
	if r.Error == "" || calls.Load() != 1 {
		t.Fatal(r, calls.Load())
	}
}
func TestRedirectIsNotFollowedAndNoTokenForward(t *testing.T) {
	var redirected atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer dest.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dest.URL, 302) }))
	defer s.Close()
	out := Perform(context.Background(), fixtureClient(), s.URL, Request{Path: "/redirect"}, Auth{Mode: "bearer", TokenVariable: "TOKEN", Prefix: "Bearer "}, map[string]string{"TOKEN": "secret"}, nil)
	if out.Status != 302 || redirected.Load() != 0 {
		t.Fatal(out)
	}
}
func TestSSEMultilineLimitsAndRealProgress(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fmt.Fprint(w, "\ufeff: ping\nid: 7\nevent: delta\ndata: 你\ndata: 好\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: second\n\ndata: third\n\n")
	}))
	defer s.Close()
	events := []Event{}
	r := Perform(context.Background(), fixtureClient(), s.URL, Request{Mode: "sse", Path: "/events", MaxEvents: 2, Assertions: []Assertion{{Type: "sse_count", Expected: "2"}}}, Auth{}, nil, func(v Event) { events = append(events, v) })
	if r.Error != "" || r.StopReason != "event_limit" || len(events) != 2 || events[0].Data != "你\n好" || events[1].ID != "7" || !r.Checks[0].Passed || calls.Load() != 1 {
		t.Fatal(r, events)
	}
}
func TestSSEDeadlineAndCancelNeverReconnect(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: started\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	r := Perform(ctx, fixtureClient(), s.URL, Request{Mode: "sse", Path: "/events"}, Auth{}, nil, nil)
	if r.StopReason != "time_limit" || r.Error != "" || len(r.Events) != 1 || calls.Load() != 1 {
		t.Fatal(r)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	r = Perform(ctx2, fixtureClient(), s.URL, Request{Mode: "sse", Path: "/events"}, Auth{}, nil, func(Event) { cancel2() })
	if !r.OutcomeUnknown || calls.Load() != 2 {
		t.Fatal(r)
	}
}
func TestBoundsTransportAndCrossOriginPaths(t *testing.T) {
	for _, path := range []string{"https://other.invalid/x", "//other/x", "/../x", "/%2e%2e/x", "/foo\\bar", "/x#other"} {
		if _, e := Join("https://trusted.invalid", path); e == nil {
			t.Fatal(path)
		}
	}
	for _, ip := range []string{"127.0.0.1", "::1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1"} {
		if AllowedIP(net.ParseIP(ip)) {
			t.Fatal(ip)
		}
	}
	if !AllowedIP(net.ParseIP("192.168.0.6")) {
		t.Fatal("legitimate bound intranet targets should work")
	}
	client := Client()
	if _, e := client.Get("http://127.0.0.1:1"); e == nil {
		t.Fatal("loopback dial accepted")
	}
	if e := Validate(&Request{Path: "/", Headers: map[string]string{"Host": "other"}}); e == nil {
		t.Fatal("host override accepted")
	}
	if e := Validate(&Request{Path: "/", Headers: map[string]string{"X-Key": "x\r\nSecret: y"}}); e == nil {
		t.Fatal("header injection accepted")
	}
}
func TestResponseLimitAndSSEInvalidType(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", MaxBody+10)) }))
	defer s.Close()
	r := Perform(context.Background(), fixtureClient(), s.URL, Request{Path: "/"}, Auth{}, nil, nil)
	if !r.Truncated || len(r.Body) != MaxBody {
		t.Fatal(r.Truncated, len(r.Body))
	}
	r = Perform(context.Background(), fixtureClient(), s.URL, Request{Path: "/", Mode: "sse"}, Auth{}, nil, nil)
	if r.Error == "" {
		t.Fatal(r)
	}
	if _, e := ReadSSE(strings.NewReader("data: "+strings.Repeat("x", 70<<10)+"\n\n"), 10, func(Event) {}); e == nil {
		t.Fatal("unbounded SSE event")
	}
}

func TestEncodedPathAndOverlappingSecrets(t *testing.T) {
	out, e := Join("https://trusted.invalid/base", "/orders/a%2Fb?q=1")
	if e != nil || out != "https://trusted.invalid/base/orders/a%2Fb?q=1" {
		t.Fatal(out, e)
	}
	text := redactText("secret-token", []string{"secret", "secret-token"})
	if text != "[REDACTED]" {
		t.Fatal(text)
	}
}
