package extensions

import (
	"context"
	"encoding/json"
	"logplatform/pkg/extensionclient"
	p "logplatform/pkg/extensionprotocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateBrokerScopesCapabilitiesAndIdentity(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err = m.Install(testPackage(t, nil, "")); err != nil {
		t.Fatal(err)
	}
	if err = m.Configure("fixture-plugin", map[string]any{"password": "fixture-only"}); err != nil {
		t.Fatal(err)
	}
	if err = m.Enable("fixture-plugin"); err != nil {
		t.Fatal(err)
	}
	file, closeBroker, err := m.StartBroker(filepath.Join(t.TempDir(), "broker"), []string{"fixture.read.v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer closeBroker()
	if !filepath.IsAbs(file) {
		t.Fatal("relative backend configuration")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("token file permissions: %v", err)
	}
	t.Setenv(extensionclient.ConfigEnv, file)
	c, err := extensionclient.FromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := c.Invoke(ctx, "fixture-plugin", p.Request{Capability: "fixture.read.v1", Operation: "read", Actor: p.Actor{User: "forged", Role: "member"}})
	if err != nil {
		t.Fatal(err)
	}
	var actor p.Actor
	if json.Unmarshal(out.Data, &actor) != nil || actor.User != "platform-build" || actor.Role != "admin" {
		t.Fatal(string(out.Data))
	}
	if _, err = c.Invoke(ctx, "fixture-plugin", p.Request{Capability: "unlisted.admin.v1"}); err == nil {
		t.Fatal("out-of-scope capability forwarded")
	}
	if err = m.Disable("fixture-plugin"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Invoke(ctx, "fixture-plugin", p.Request{Capability: "fixture.read.v1"}); err == nil {
		t.Fatal("disabled provider accepted")
	}
}
