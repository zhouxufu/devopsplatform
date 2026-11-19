package deploy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSSHExecutionDeadlineAndCancellation(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelEarly), func(t *testing.T) {
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			signer, _ := ssh.NewSignerFromKey(key)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				defer conn.Close()
				cfg := &ssh.ServerConfig{NoClientAuth: true}
				cfg.AddHostKey(signer)
				server, channels, requests, e := ssh.NewServerConn(conn, cfg)
				if e != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for c := range channels {
					ch, reqs, e := c.Accept()
					if e != nil {
						continue
					}
					go func() {
						defer ch.Close()
						for r := range reqs {
							r.Reply(true, nil)
						}
					}()
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelEarly {
				go func() { time.Sleep(40 * time.Millisecond); cancel() }()
			}
			start := time.Now()
			_, err = SSHExecContext(ctx, Host{Addr: listener.Addr().String(), User: "fixture", Password: "fixture", HostKey: ssh.FingerprintSHA256(signer.PublicKey())}, "blocked", 150*time.Millisecond)
			if err == nil || time.Since(start) > 2*time.Second {
				t.Fatalf("SSH command not bounded: %v %s", err, time.Since(start))
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("SSH transport remained open")
			}
		})
	}
}

func TestNormalizeSSHAddress(t *testing.T) {
	tests := map[string]string{
		"example.internal": "example.internal:22",
		"127.0.0.1":        "127.0.0.1:22",
		"host:2222":        "host:2222",
		"::1":              "[::1]:22",
		"[::1]":            "[::1]:22",
		"[::1]:2222":       "[::1]:2222",
	}
	for input, want := range tests {
		got, err := normalizeSSHAddress(input)
		if err != nil {
			t.Fatalf("normalizeSSHAddress(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("normalizeSSHAddress(%q) = %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{"", "host:not-a-port", "host:70000", "bad host"} {
		if _, err := normalizeSSHAddress(input); err == nil {
			t.Errorf("normalizeSSHAddress(%q) unexpectedly succeeded", input)
		}
	}
}

func TestScanSSHHostKey(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		cfg := &ssh.ServerConfig{NoClientAuth: true}
		cfg.AddHostKey(signer)
		_, _, _, _ = ssh.NewServerConn(conn, cfg)
	}()

	scan, err := ScanSSHHostKey(listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Algorithm != ssh.KeyAlgoED25519 {
		t.Errorf("algorithm = %q, want %q", scan.Algorithm, ssh.KeyAlgoED25519)
	}
	if want := ssh.FingerprintSHA256(signer.PublicKey()); scan.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", scan.Fingerprint, want)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("test SSH server did not stop")
	}
}
