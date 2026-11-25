package dubborpc

import (
	"context"
	"testing"
)

func TestProviderAddressBoundary(t *testing.T) {
	for _, v := range []string{"http://example.com:80", "dubbo://u:p@example.com:20880", "dubbo://example.com:20880/path", "dubbo://example.com:20880?retries=1", "dubbo://example.com", "dubbo://example.com:65536"} {
		if _, e := Endpoint(v); e == nil {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"dubbo://127.0.0.1:20880", "dubbo://[::1]:20880", "dubbo://169.254.169.254:80", "dubbo://100.100.100.200:80"} {
		if _, e := Resolve(context.Background(), v); e == nil {
			t.Fatal("unsafe address", v)
		}
	}
	if _, e := Resolve(context.Background(), "dubbo://192.0.2.10:20880"); e != nil {
		t.Fatal(e)
	}
}
