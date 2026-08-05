package service

import (
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNormalizeCentralManagementDomain(t *testing.T) {
	valid, err := normalizeCentralManagementDomain(" RP2.Wakeup-AI.Top ")
	if err != nil || valid != "rp2.wakeup-ai.top" {
		t.Fatalf("normalized domain = %q, %v", valid, err)
	}
	for _, input := range []string{
		"https://rp2.wakeup-ai.top",
		"rp2.wakeup-ai.top:2083",
		"*.wakeup-ai.top",
		"rp2.wakeup-ai.top; return 200",
		"127.0.0.1",
	} {
		if _, err := normalizeCentralManagementDomain(input); err == nil {
			t.Errorf("domain %q should be rejected", input)
		}
	}
}

func TestBuildCentralManagementNginxConfigKeepsManagementPathAndIsIsolated(t *testing.T) {
	body := buildCentralManagementNginxConfig(
		"rp2.wakeup-ai.top", 2083, "/otJusMQxf1caAFzjHk7pVC/",
		"/etc/line-panel/central-management/versions/a/origin.crt",
		"/etc/line-panel/central-management/versions/a/origin.key",
	)
	for _, required := range []string{
		"listen 2083 ssl http2;",
		"server_name rp2.wakeup-ai.top;",
		"location ^~ /otJusMQxf1caAFzjHk7pVC/",
		"proxy_pass http://127.0.0.1:2053;",
		"location / {",
		"return 404;",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("Nginx config is missing %q:\n%s", required, body)
		}
	}
	if strings.Contains(body, "x-ui-line-") || strings.Contains(body, ":8443") {
		t.Fatalf("central config must not contain line configuration: %s", body)
	}
}

func TestApplyCentralManagementNginxConfigRestoresAndReloadsPreviousConfig(t *testing.T) {
	executor := newFakeNginxExecutor()
	executor.files[centralManagementNginxConfigPath] = []byte("old config")
	executor.failures["systemctl reload nginx"] = errors.New("systemctl failed")
	executor.failures["service nginx reload"] = errors.New("service failed")

	err := applyCentralManagementNginxConfig("new config", executor)
	if err == nil || !strings.Contains(err.Error(), "restored config could not reload") {
		t.Fatalf("error = %v, want explicit restored-config reload failure", err)
	}
	if got := string(executor.files[centralManagementNginxConfigPath]); got != "old config" {
		t.Fatalf("restored config = %q, want old config", got)
	}
	if len(executor.commands) < 5 {
		t.Fatalf("commands = %v, want test/reload plus restore test/reload", executor.commands)
	}
}

func TestCloudflareHTTPSPorts(t *testing.T) {
	for _, port := range []int{443, 2053, 2083, 2087, 2096, 8443} {
		if !isCloudflareHTTPSPort(port) {
			t.Errorf("port %d should be allowed", port)
		}
	}
	for _, port := range []int{80, 444, 3000, 65535} {
		if isCloudflareHTTPSPort(port) {
			t.Errorf("port %d should be rejected", port)
		}
	}
}

func TestWaitForCentralManagementListenerRetriesOnlyConnectionRefused(t *testing.T) {
	start := time.Unix(1700000000, 0)
	now := start
	var sleeps []time.Duration
	attempt := 0
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	err := waitForCentralManagementListener("127.0.0.1:2083", start.Add(time.Second), func(_, _ string, _ time.Duration) (net.Conn, error) {
		attempt++
		if attempt < 3 {
			return nil, syscall.ECONNREFUSED
		}
		return client, nil
	}, func() time.Time { return now }, func(d time.Duration) { sleeps = append(sleeps, d); now = now.Add(d) })
	if err != nil {
		t.Fatalf("wait error: %v", err)
	}
	if attempt != 3 || len(sleeps) != 2 {
		t.Fatalf("attempts=%d sleeps=%v, want 3 attempts and 2 retries", attempt, sleeps)
	}
}

func TestWaitForCentralManagementListenerFailsImmediatelyForOtherErrors(t *testing.T) {
	start := time.Unix(1700000000, 0)
	attempt := 0
	sleeps := 0
	err := waitForCentralManagementListener("127.0.0.1:2083", start.Add(time.Second), func(_, _ string, _ time.Duration) (net.Conn, error) {
		attempt++
		return nil, syscall.EACCES
	}, func() time.Time { return start }, func(time.Duration) { sleeps++ })
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("error = %v, want EACCES", err)
	}
	if attempt != 1 || sleeps != 0 {
		t.Fatalf("attempts=%d sleeps=%d, want immediate failure", attempt, sleeps)
	}
}

func TestWaitForCentralManagementListenerTimesOutWithConnectionRefused(t *testing.T) {
	start := time.Unix(1700000000, 0)
	now := start
	attempt := 0
	err := waitForCentralManagementListener("127.0.0.1:2083", start.Add(200*time.Millisecond), func(_, _ string, _ time.Duration) (net.Conn, error) {
		attempt++
		return nil, syscall.ECONNREFUSED
	}, func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	if !errors.Is(err, syscall.ECONNREFUSED) || !strings.Contains(err.Error(), "3 connection attempts") {
		t.Fatalf("error = %v, want bounded refused timeout", err)
	}
	if attempt != 3 {
		t.Fatalf("attempts=%d, want 3", attempt)
	}
}
