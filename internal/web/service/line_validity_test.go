package service

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/op/go-logging"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	xuilogger "github.com/mhsanaei/3x-ui/v3/internal/logger"
)

var lineValidityLoggerOnce sync.Once

func setupLineValidityDB(t *testing.T) {
	t.Helper()
	lineValidityLoggerOnce.Do(func() { xuilogger.InitLogger(logging.ERROR) })
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	if err := database.InitDB(filepath.Join(dbDir, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		if err := database.CloseDB(); err != nil {
			t.Logf("CloseDB warning: %v", err)
		}
	})
}

func TestNormalizeLineValidity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	future := now.Add(time.Hour).Unix()
	start := now.Add(30 * time.Minute).Unix()

	if _, _, err := normalizeLineValidity(nil, &future, now); err != nil {
		t.Fatalf("future expiry rejected: %v", err)
	}
	if _, _, err := normalizeLineValidity(&start, &future, now); err != nil {
		t.Fatalf("ordered validity rejected: %v", err)
	}
	past := now.Add(-time.Second).Unix()
	if _, _, err := normalizeLineValidity(nil, &past, now); err == nil {
		t.Fatal("past expiry accepted")
	}
	if _, _, err := normalizeLineValidity(&future, &start, now); err == nil {
		t.Fatal("expiry before start accepted")
	}
	if got := lineInitialStatus(start, now); got != lineStatusScheduled {
		t.Fatalf("future line status = %q, want %q", got, lineStatusScheduled)
	}
}

func TestUpdateLineValidityDoesNotMutateRuntime(t *testing.T) {
	setupLineValidityDB(t)
	now := time.Now()
	inbound := &model.Inbound{Tag: "line-1-in", Enable: true, Port: 30001, Protocol: model.VLESS}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	line := &model.LineProfile{
		Name:        "validity-test",
		Type:        LineTypeReality,
		Status:      "active",
		InboundId:   &inbound.Id,
		OutboundTag: "line-1-out",
		ConfigJSON:  `{"realitySni":"example.com"}`,
	}
	if err := database.GetDB().Create(line).Error; err != nil {
		t.Fatalf("create line: %v", err)
	}
	if err := database.GetDB().Create(&model.LineOutbound{LineId: line.Id, Tag: line.OutboundTag, Type: "socks5", Host: "127.0.0.1", Port: 1080, Enabled: true}).Error; err != nil {
		t.Fatalf("create outbound: %v", err)
	}

	validUntil := now.Add(48 * time.Hour).Unix()
	if _, err := (&LineService{}).UpdateLineValidity(line.Id, LineValidityRequest{ValidUntil: &validUntil}); err != nil {
		t.Fatalf("UpdateLineValidity: %v", err)
	}

	var got model.LineProfile
	if err := database.GetDB().First(&got, line.Id).Error; err != nil {
		t.Fatalf("load line: %v", err)
	}
	if got.Status != "active" || got.InboundId == nil || *got.InboundId != inbound.Id {
		t.Fatalf("runtime state changed: %#v", got)
	}
	if got.ConfigJSON != line.ConfigJSON || got.ValidUntil != validUntil {
		t.Fatalf("line update = %#v", got)
	}
	var gotInbound model.Inbound
	if err := database.GetDB().First(&gotInbound, inbound.Id).Error; err != nil || !gotInbound.Enable {
		t.Fatalf("inbound changed after extension: %#v, %v", gotInbound, err)
	}
}

func TestExpireLineCutsRuntimeButPreservesLineMetadata(t *testing.T) {
	setupLineValidityDB(t)
	now := time.Now()
	inbound := &model.Inbound{Tag: "line-1-in", Enable: true, Port: 30001, Protocol: model.VLESS}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	line := &model.LineProfile{
		Name:        "expired-line",
		Type:        LineTypeReality,
		Status:      "active",
		InboundId:   &inbound.Id,
		OutboundTag: "line-1-out",
		ValidUntil:  now.Add(-time.Minute).Unix(),
		ConfigJSON:  `{}`,
	}
	if err := database.GetDB().Create(line).Error; err != nil {
		t.Fatalf("create line: %v", err)
	}
	outbound := &model.LineOutbound{LineId: line.Id, Tag: line.OutboundTag, Type: "socks5", Host: "127.0.0.1", Port: 1080, Enabled: true}
	if err := database.GetDB().Create(outbound).Error; err != nil {
		t.Fatalf("create outbound: %v", err)
	}

	restart, err := (&LineService{}).expireLine(line.Id, now)
	if err != nil || !restart {
		t.Fatalf("expireLine = restart:%v err:%v", restart, err)
	}
	var got model.LineProfile
	if err := database.GetDB().First(&got, line.Id).Error; err != nil {
		t.Fatalf("load expired line: %v", err)
	}
	if got.Status != lineStatusExpired || !got.ManualReenableRequired || got.InboundId != nil || got.ExpiredAt == 0 {
		t.Fatalf("expiry lifecycle = %#v", got)
	}
	var deletedInbound model.Inbound
	if err := database.GetDB().First(&deletedInbound, inbound.Id).Error; err == nil {
		t.Fatal("expired line inbound still exists")
	}
	var keptOutbound model.LineOutbound
	if err := database.GetDB().Where("line_id = ?", line.Id).First(&keptOutbound).Error; err != nil || keptOutbound.Id != outbound.Id {
		t.Fatalf("line outbound was not preserved: %#v, %v", keptOutbound, err)
	}
	if err := validateLineCanApply(got, now); err == nil {
		t.Fatal("expired line can still serve")
	}
}

func TestExpireShadowsocksLineRemovesManagedClient(t *testing.T) {
	setupLineValidityDB(t)
	now := time.Now()
	password := randomShadowsocksClientKey(defaultShadowsocksMethod)
	inbound := &model.Inbound{Tag: "line-1-in", Enable: true, Listen: "0.0.0.0", Port: 30080, Protocol: model.Shadowsocks, Settings: mustJSON(map[string]any{
		"method":  defaultShadowsocksMethod,
		"clients": []map[string]any{{"email": "line-1-user", "password": password}},
	})}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("create Shadowsocks inbound: %v", err)
	}
	line := &model.LineProfile{
		Name:        "expired-shadowsocks-line",
		Type:        LineTypeShadowsocks,
		Status:      "active",
		InboundId:   &inbound.Id,
		OutboundTag: "line-1-out",
		ValidUntil:  now.Add(-time.Minute).Unix(),
		ConfigJSON:  `{}`,
	}
	if err := database.GetDB().Create(line).Error; err != nil {
		t.Fatalf("create Shadowsocks line: %v", err)
	}
	client := &model.ClientRecord{Email: lineManagedClientEmail(line.Id), SubID: lineManagedClientSubID(line.Id), Password: password, Enable: true}
	if err := database.GetDB().Create(client).Error; err != nil {
		t.Fatalf("create managed Shadowsocks client: %v", err)
	}
	if err := database.GetDB().Create(&model.ClientInbound{ClientId: client.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatalf("link managed Shadowsocks client: %v", err)
	}

	restart, err := (&LineService{}).expireLine(line.Id, now)
	if err != nil || !restart {
		t.Fatalf("expire Shadowsocks line = restart:%v err:%v", restart, err)
	}
	var deletedInbound model.Inbound
	if err := database.GetDB().First(&deletedInbound, inbound.Id).Error; err == nil {
		t.Fatal("expired Shadowsocks inbound still exists")
	}
	var deletedClient model.ClientRecord
	if err := database.GetDB().First(&deletedClient, client.Id).Error; err == nil {
		t.Fatal("expired Shadowsocks managed client still exists")
	}
	var deletedLink model.ClientInbound
	if err := database.GetDB().Where("client_id = ? AND inbound_id = ?", client.Id, inbound.Id).First(&deletedLink).Error; err == nil {
		t.Fatal("expired Shadowsocks managed client link still exists")
	}
}

func TestRenewLineRequiresExpiredManualLock(t *testing.T) {
	setupLineValidityDB(t)
	now := time.Now()
	line := &model.LineProfile{
		Name:                   "renew-test",
		Type:                   LineTypeReality,
		Status:                 lineStatusExpired,
		ManualReenableRequired: true,
		ValidUntil:             now.Add(-time.Minute).Unix(),
		ConfigJSON:             `{}`,
	}
	if err := database.GetDB().Create(line).Error; err != nil {
		t.Fatalf("create line: %v", err)
	}
	validUntil := now.Add(24 * time.Hour).Unix()
	if _, err := (&LineService{}).RenewLine(line.Id, LineValidityRequest{ValidUntil: &validUntil}); err != nil {
		t.Fatalf("RenewLine: %v", err)
	}
	var got model.LineProfile
	if err := database.GetDB().First(&got, line.Id).Error; err != nil {
		t.Fatalf("load renewed line: %v", err)
	}
	if got.ManualReenableRequired || got.Status != "pending_apply" || got.ValidUntil != validUntil || got.ValidFrom < now.Add(-time.Minute).Unix() {
		t.Fatalf("renewed lifecycle = %#v", got)
	}
	if _, err := (&LineService{}).RenewLine(line.Id, LineValidityRequest{ValidUntil: &validUntil}); err == nil {
		t.Fatal("active renewal was accepted without a new expiry")
	}
}

func TestUpdateLineValidityAllowsLongTerm(t *testing.T) {
	setupLineValidityDB(t)
	line := &model.LineProfile{
		Name:       "long-term-validity",
		Type:       LineTypeReality,
		Status:     "active",
		ValidUntil: time.Now().Add(24 * time.Hour).Unix(),
		ConfigJSON: `{}`,
	}
	if err := database.GetDB().Create(line).Error; err != nil {
		t.Fatalf("create line: %v", err)
	}
	longTerm := int64(0)
	if _, err := (&LineService{}).UpdateLineValidity(line.Id, LineValidityRequest{ValidUntil: &longTerm}); err != nil {
		t.Fatalf("UpdateLineValidity(long term): %v", err)
	}
	var got model.LineProfile
	if err := database.GetDB().First(&got, line.Id).Error; err != nil {
		t.Fatalf("load line: %v", err)
	}
	if got.ValidUntil != 0 || got.Status != "active" || got.ManualReenableRequired {
		t.Fatalf("long-term update = %#v", got)
	}
}
