package service

import (
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func seedSubscriptionLine(t *testing.T, lineType string, config map[string]string) *model.LineProfile {
	t.Helper()
	inbound := &model.Inbound{
		Tag:      "line-subscription-in",
		Enable:   true,
		Port:     30001,
		Protocol: model.VLESS,
		Settings: `{"clients":[{"id":"11111111-2222-3333-4444-555555555555"}]}`,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	line := &model.LineProfile{
		Name:        "test relay",
		Type:        lineType,
		Status:      "active",
		InboundId:   &inbound.Id,
		EntryHost:   "entry.example.com",
		EntryPort:   443,
		OutboundTag: "line-subscription-out",
		ConfigJSON:  encodeLineConfig(config),
	}
	if err := database.GetDB().Create(line).Error; err != nil {
		t.Fatalf("create line: %v", err)
	}
	return line
}

func TestPublicLineClashSubscriptionYAMLAndTokenRotation(t *testing.T) {
	setupLineValidityDB(t)
	line := seedSubscriptionLine(t, LineTypeCloudflare, map[string]string{
		"wsPath": "/relay-ws",
	})

	firstToken, firstRecord, err := getOrCreateLineSubscriptionToken(line.Id, false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if firstRecord.TokenCiphertext == firstToken || strings.Contains(firstRecord.TokenCiphertext, firstToken) {
		t.Fatalf("subscription token must not be stored as plaintext")
	}
	body, _, err := (&LineService{}).GetPublicLineClashSubscription(firstToken)
	if err != nil {
		t.Fatalf("public subscription: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse YAML: %v\n%s", err, body)
	}
	proxies, ok := document["proxies"].([]any)
	if !ok || len(proxies) != 1 {
		t.Fatalf("proxies = %#v", document["proxies"])
	}
	proxy, _ := proxies[0].(map[string]any)
	if proxy["type"] != "vless" || proxy["network"] != "ws" || proxy["servername"] != "entry.example.com" {
		t.Fatalf("Cloudflare proxy = %#v", proxy)
	}
	wsOpts, _ := proxy["ws-opts"].(map[string]any)
	if wsOpts["path"] != "/relay-ws" {
		t.Fatalf("ws options = %#v", wsOpts)
	}

	secondToken, _, err := getOrCreateLineSubscriptionToken(line.Id, true)
	if err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	if secondToken == firstToken {
		t.Fatal("rotated subscription token did not change")
	}
	if _, _, err := (&LineService{}).GetPublicLineClashSubscription(firstToken); err == nil {
		t.Fatal("old subscription token remained valid after rotation")
	}
	if _, _, err := (&LineService{}).GetPublicLineClashSubscription(secondToken); err != nil {
		t.Fatalf("new subscription token: %v", err)
	}
}

func TestRealityClashSubscriptionDoesNotLeakPrivateKeyAndExpires(t *testing.T) {
	setupLineValidityDB(t)
	line := seedSubscriptionLine(t, LineTypeReality, map[string]string{
		"realityPublicKey":   "public-key-only",
		"realityPrivateKey":  "private-key-must-not-leak",
		"realityShortId":     "abcd1234",
		"realitySni":         "www.example.com",
		"realityFingerprint": "chrome",
		"realityFlow":        "xtls-rprx-vision",
	})
	token, _, err := getOrCreateLineSubscriptionToken(line.Id, false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	body, _, err := (&LineService{}).GetPublicLineClashSubscription(token)
	if err != nil {
		t.Fatalf("public Reality subscription: %v", err)
	}
	if strings.Contains(string(body), "private-key-must-not-leak") {
		t.Fatalf("private Reality key leaked in YAML: %s", body)
	}
	if !strings.Contains(string(body), "reality-opts") || !strings.Contains(string(body), "public-key-only") {
		t.Fatalf("Reality YAML is missing required client settings: %s", body)
	}
	if err := database.GetDB().Model(line).Update("valid_until", time.Now().Add(-time.Minute).Unix()).Error; err != nil {
		t.Fatalf("expire line: %v", err)
	}
	if _, _, err := (&LineService{}).GetPublicLineClashSubscription(token); err == nil {
		t.Fatal("expired line still served a public subscription")
	}
}

func TestShadowsocksClashSubscriptionYAML(t *testing.T) {
	setupLineValidityDB(t)
	serverKey := randomShadowsocksClientKey(defaultShadowsocksMethod)
	clientKey := randomShadowsocksClientKey(defaultShadowsocksMethod)
	inbound := &model.Inbound{
		Tag:      "line-ss-in",
		Enable:   true,
		Listen:   "0.0.0.0",
		Port:     30080,
		Protocol: model.Shadowsocks,
		Settings: mustJSON(map[string]any{
			"method":   defaultShadowsocksMethod,
			"password": serverKey,
			"clients":  []map[string]any{{"email": "line-1-user", "password": clientKey}},
		}),
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("create Shadowsocks inbound: %v", err)
	}
	line := &model.LineProfile{
		Name:        "ss relay",
		Type:        LineTypeShadowsocks,
		Status:      "active",
		InboundId:   &inbound.Id,
		EntryHost:   "203.0.113.10",
		EntryPort:   30080,
		OutboundTag: "line-ss-out",
		ConfigJSON:  `{}`,
	}
	if err := database.GetDB().Create(line).Error; err != nil {
		t.Fatalf("create Shadowsocks line: %v", err)
	}
	client := &model.ClientRecord{Email: lineManagedClientEmail(line.Id), SubID: lineManagedClientSubID(line.Id), Password: clientKey, Enable: true}
	if err := database.GetDB().Create(client).Error; err != nil {
		t.Fatalf("create managed Shadowsocks client: %v", err)
	}
	if err := database.GetDB().Create(&model.ClientInbound{ClientId: client.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatalf("link managed Shadowsocks client: %v", err)
	}

	body, _, err := buildLineClashSubscriptionYAML(line.Id)
	if err != nil {
		t.Fatalf("build Shadowsocks YAML: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse Shadowsocks YAML: %v\n%s", err, body)
	}
	proxies, ok := document["proxies"].([]any)
	if !ok || len(proxies) != 1 {
		t.Fatalf("Shadowsocks proxies = %#v", document["proxies"])
	}
	proxy, _ := proxies[0].(map[string]any)
	if proxy["type"] != "ss" || proxy["server"] != "203.0.113.10" || proxy["port"] != uint64(30080) || proxy["cipher"] != defaultShadowsocksMethod || proxy["password"] != serverKey+":"+clientKey || proxy["udp"] != false {
		t.Fatalf("Shadowsocks proxy = %#v", proxy)
	}
	if _, hasUUID := proxy["uuid"]; hasUUID {
		t.Fatalf("Shadowsocks proxy leaked VLESS UUID: %#v", proxy)
	}
}

func TestBuildNginxPlanIncludesSubscriptionLocation(t *testing.T) {
	setupLineValidityDB(t)
	plan := buildNginxPlan(model.LineProfile{
		Id:        7,
		Type:      LineTypeCloudflare,
		EntryHost: "entry.example.com",
		EntryPort: 8443,
	}, map[string]string{
		"wsPath":        "/relay-ws",
		"localXrayPort": "30007",
	})
	if !strings.Contains(plan, "location ^~ /rp/sub/") || !strings.Contains(plan, "access_log off;") || !strings.Contains(plan, "proxy_pass http://127.0.0.1:2053") {
		t.Fatalf("subscription route missing from Nginx plan:\n%s", plan)
	}
	if !strings.Contains(plan, "location /relay-ws") {
		t.Fatalf("WebSocket route missing from Nginx plan:\n%s", plan)
	}
}
