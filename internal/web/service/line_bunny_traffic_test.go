package service

import (
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestBunnyNginxPlanUsesOriginHostAndPort(t *testing.T) {
	line := model.LineProfile{
		Id:        7,
		Type:      LineTypeBunny,
		EntryHost: "wakeup01.b-cdn.net",
		EntryPort: 443,
	}
	config := map[string]string{
		"originHost":    "origin.wakeup-ai.top",
		"originPort":    "8443",
		"nginxCertFile": "/etc/letsencrypt/live/origin.wakeup-ai.top/fullchain.pem",
		"nginxKeyFile":  "/etc/letsencrypt/live/origin.wakeup-ai.top/privkey.pem",
		"wsPath":        "/relay",
		"localXrayPort": "30007",
	}

	plan := buildNginxPlan(line, config)
	for _, want := range []string{
		"listen 8443 ssl http2;",
		"server_name origin.wakeup-ai.top;",
		"proxy_pass http://127.0.0.1:30007;",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("Nginx plan missing %q:\n%s", want, plan)
		}
	}
	if strings.Contains(plan, "wakeup01.b-cdn.net") {
		t.Fatalf("Bunny public entry must not be used as the Nginx server name:\n%s", plan)
	}
}

func TestValidateBunnyConfig(t *testing.T) {
	valid := map[string]string{
		"originHost": "origin.wakeup-ai.top",
		"originPort": "8443",
		"acmeEmail":  "admin@wakeup-ai.top",
	}
	if err := validateBunnyConfig(valid); err != nil {
		t.Fatalf("validateBunnyConfig(valid) error = %v", err)
	}

	invalid := map[string]string{
		"originHost": "153.75.235.141",
		"originPort": "8443",
		"acmeEmail":  "admin@wakeup-ai.top",
	}
	if err := validateBunnyConfig(invalid); err == nil {
		t.Fatal("validateBunnyConfig() accepted an IP address for the origin host")
	}
}

func TestRecordLineTrafficDeltasUsesSingleXrayPollInterval(t *testing.T) {
	lineTrafficSnapshotStore.Lock()
	lineTrafficSnapshotStore.lastCollectedAt = time.Time{}
	lineTrafficSnapshotStore.snapshots = make(map[int]LineTrafficSnapshot)
	lineTrafficSnapshotStore.Unlock()

	startedAt := time.Unix(1_700_000_000, 0)
	if got := RecordLineTrafficDeltas(nil, startedAt); len(got) != 0 {
		t.Fatalf("first poll snapshots = %#v, want none", got)
	}

	snapshots := RecordLineTrafficDeltas([]*xray.Traffic{
		{Tag: "line-9-in", IsInbound: true, Up: 200, Down: 400},
		{Tag: "line-9-out", IsOutbound: true, Up: 600, Down: 800},
	}, startedAt.Add(2*time.Second))
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	got := snapshots[0]
	if got.LineID != 9 || got.InboundSpeedUp != 100 || got.InboundSpeedDown != 200 || got.OutboundSpeedUp != 300 || got.OutboundSpeedDown != 400 {
		t.Fatalf("snapshot = %#v, want rates from the two-second Xray poll interval", got)
	}
}
