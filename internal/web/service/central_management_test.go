package service

import (
	"errors"
	"strings"
	"testing"
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
