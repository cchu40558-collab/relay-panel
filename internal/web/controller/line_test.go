package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func TestLineController_CreateListGetUpdate(t *testing.T) {
	newHostTestDB(t)
	engine := gin.New()
	NewLineController(engine.Group("/panel/api"))

	create := doHostReq(t, engine, http.MethodPost, "/panel/api/lines", map[string]any{
		"type":             service.LineTypeCloudflare,
		"name":             "cf-main",
		"entryHost":        "proxy.example.com",
		"entryPort":        8443,
		"outboundType":     "socks5",
		"outboundHost":     "res.example.net",
		"outboundPort":     1080,
		"outboundUsername": "alice",
		"outboundPassword": "secret",
		"config":           map[string]string{"wsPath": "/ws"},
	})
	if !create.Success {
		t.Fatalf("create not successful: %s", create.Msg)
	}

	var detail service.LineDetail
	if err := json.Unmarshal(create.Obj, &detail); err != nil {
		t.Fatalf("decode created detail: %v", err)
	}
	if detail.Id == 0 || detail.Status != "pending_apply" || detail.Outbound == nil || detail.Plan == nil {
		t.Fatalf("created detail = %+v", detail)
	}
	if detail.Outbound.Password != "" {
		t.Fatalf("outbound password leaked in response")
	}
	if detail.Config["localXrayPort"] == "" || detail.Plan.Nginx == "" || len(detail.Logs) == 0 {
		t.Fatalf("created plan not populated: config=%+v plan=%+v logs=%+v", detail.Config, detail.Plan, detail.Logs)
	}

	apply := doHostReq(t, engine, http.MethodPost, "/panel/api/lines/1/apply", map[string]any{})
	if !apply.Success {
		t.Fatalf("apply not successful: %s", apply.Msg)
	}
	var applied service.LineDetail
	if err := json.Unmarshal(apply.Obj, &applied); err != nil {
		t.Fatalf("decode applied detail: %v", err)
	}
	if applied.Status != "pending_check" || applied.InboundId == nil || len(applied.Logs) < 5 {
		t.Fatalf("applied detail = %+v", applied)
	}
	var inbound model.Inbound
	if err := database.GetDB().First(&inbound, *applied.InboundId).Error; err != nil {
		t.Fatalf("load applied inbound: %v", err)
	}
	if inbound.Protocol != model.VLESS || inbound.Listen != "127.0.0.1" || inbound.Port != 30001 || inbound.Tag != "line-1-in" {
		t.Fatalf("applied inbound = %+v", inbound)
	}
	var cfStream map[string]any
	if err := json.Unmarshal([]byte(inbound.StreamSettings), &cfStream); err != nil {
		t.Fatalf("decode cloudflare stream: %v", err)
	}
	if cfStream["network"] != "ws" || cfStream["security"] != "none" {
		t.Fatalf("cloudflare stream = %+v", cfStream)
	}
	wsSettings, _ := cfStream["wsSettings"].(map[string]any)
	if wsSettings["path"] != "/ws" {
		t.Fatalf("cloudflare ws settings = %+v", wsSettings)
	}
	var template model.Setting
	if err := database.GetDB().Where("key = ?", "xrayTemplateConfig").First(&template).Error; err != nil {
		t.Fatalf("load xray template: %v", err)
	}
	assertXrayTemplateHasLineRoute(t, template.Value, "line-1-in", "line-1-out")

	cfShare := doHostReq(t, engine, http.MethodGet, "/panel/api/lines/1/share", nil)
	if !cfShare.Success {
		t.Fatalf("share cloudflare not successful: %s", cfShare.Msg)
	}
	var cfShareResp service.LineShareResponse
	if err := json.Unmarshal(cfShare.Obj, &cfShareResp); err != nil {
		t.Fatalf("decode cloudflare share: %v", err)
	}
	if len(cfShareResp.Links) != 1 ||
		cfShareResp.Links[0].Label != "VLESS WS TLS" ||
		!strings.Contains(cfShareResp.Links[0].URI, "type=ws") ||
		!strings.Contains(cfShareResp.Links[0].URI, "security=tls") ||
		!strings.Contains(cfShareResp.Links[0].URI, "path=%2Fws") ||
		!strings.Contains(cfShareResp.Links[0].URI, "host=proxy.example.com") {
		t.Fatalf("cloudflare share = %+v", cfShareResp)
	}

	list := doHostReq(t, engine, http.MethodGet, "/panel/api/lines", nil)
	var lines []model.LineProfile
	if err := json.Unmarshal(list.Obj, &lines); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(lines) != 1 || lines[0].Name != "cf-main" || lines[0].Status != "pending_check" {
		t.Fatalf("list = %+v", lines)
	}

	get := doHostReq(t, engine, http.MethodGet, "/panel/api/lines/1", nil)
	if !get.Success {
		t.Fatalf("get not successful: %s", get.Msg)
	}

	update := doHostReq(t, engine, http.MethodPost, "/panel/api/lines/1", map[string]any{
		"type":             service.LineTypeReality,
		"name":             "reality-direct",
		"entryHost":        "1.2.3.4",
		"entryPort":        443,
		"outboundType":     "http",
		"outboundHost":     "res2.example.net",
		"outboundPort":     8080,
		"outboundUsername": "bob",
		"config":           map[string]string{"realitySni": "www.itunes.com"},
	})
	if !update.Success {
		t.Fatalf("update not successful: %s", update.Msg)
	}
	var updated service.LineDetail
	if err := json.Unmarshal(update.Obj, &updated); err != nil {
		t.Fatalf("decode updated detail: %v", err)
	}
	if updated.Name != "reality-direct" || updated.Type != service.LineTypeReality || updated.Outbound == nil || updated.Outbound.Type != "http" {
		t.Fatalf("updated detail = %+v", updated)
	}
	if updated.Config["realitySni"] != "www.itunes.com" {
		t.Fatalf("updated config = %+v", updated.Config)
	}
	if updated.Plan == nil || updated.Plan.Nginx != "" || len(updated.Logs) < 2 {
		t.Fatalf("updated plan/logs not populated: plan=%+v logs=%+v", updated.Plan, updated.Logs)
	}

	applyReality := doHostReq(t, engine, http.MethodPost, "/panel/api/lines/1/apply", map[string]any{})
	if applyReality.Success {
		t.Fatalf("Reality apply with a fake residential proxy should fail its real connection check")
	}
	var failedReality service.LineDetail
	if err := json.Unmarshal(applyReality.Obj, &failedReality); err != nil {
		t.Fatalf("decode failed reality detail: %v", err)
	}
	if failedReality.Status != "apply_failed" || !strings.Contains(failedReality.LastError, "Reality real connection check failed") {
		t.Fatalf("failed Reality apply = %+v", failedReality)
	}
}

func TestLineController_LineTypesHideTrojanAndRejectCreate(t *testing.T) {
	newHostTestDB(t)
	engine := gin.New()
	NewLineController(engine.Group("/panel/api"))

	resp := doHostReq(t, engine, http.MethodGet, "/panel/api/line-types", nil)
	if !resp.Success {
		t.Fatalf("line types not successful: %s", resp.Msg)
	}
	var types []service.LineTypeInfo
	if err := json.Unmarshal(resp.Obj, &types); err != nil {
		t.Fatalf("decode line types: %v", err)
	}
	for _, item := range types {
		if item.Type == service.LineTypeTrojan {
			t.Fatalf("trojan type should be hidden in MVP: %+v", types)
		}
	}

	create := doHostReq(t, engine, http.MethodPost, "/panel/api/lines", map[string]any{
		"type":         service.LineTypeTrojan,
		"name":         "trojan",
		"entryHost":    "proxy.example.com",
		"entryPort":    443,
		"outboundType": "socks5",
		"outboundHost": "res.example.net",
		"outboundPort": 1080,
	})
	if create.Success {
		t.Fatalf("trojan create should be rejected")
	}
}

func TestLineController_ApplyValidationFailurePersistsStatusAndLog(t *testing.T) {
	newHostTestDB(t)
	engine := gin.New()
	NewLineController(engine.Group("/panel/api"))

	create := doHostReq(t, engine, http.MethodPost, "/panel/api/lines", map[string]any{
		"type":         service.LineTypeCloudflare,
		"name":         "invalid-cf",
		"entryHost":    "",
		"entryPort":    8443,
		"outboundType": "socks5",
		"outboundHost": "res.example.net",
		"outboundPort": 1080,
		"config":       map[string]string{"wsPath": "/ws"},
	})
	if !create.Success {
		t.Fatalf("create invalid draft not successful: %s", create.Msg)
	}

	apply := doHostReq(t, engine, http.MethodPost, "/panel/api/lines/1/apply", map[string]any{})
	if apply.Success {
		t.Fatalf("apply should fail validation")
	}
	var detail service.LineDetail
	if err := json.Unmarshal(apply.Obj, &detail); err != nil {
		t.Fatalf("decode failed apply detail: %v", err)
	}
	if detail.Status != "apply_failed" || !strings.Contains(detail.LastError, "entry host") {
		t.Fatalf("failed apply detail = %+v", detail)
	}

	var line model.LineProfile
	if err := database.GetDB().First(&line, 1).Error; err != nil {
		t.Fatalf("load failed line: %v", err)
	}
	if line.Status != "apply_failed" || !strings.Contains(line.LastError, "entry host") {
		t.Fatalf("persisted failed line = %+v", line)
	}

	var logs []model.LineApplyLog
	if err := database.GetDB().Where("line_id = ? AND action = ? AND level = ?", 1, "validate", "error").Find(&logs).Error; err != nil {
		t.Fatalf("load apply failure logs: %v", err)
	}
	if len(logs) == 0 || !strings.Contains(logs[0].Detail, "entry host") {
		t.Fatalf("failure logs = %+v", logs)
	}
}

func TestLineController_DeleteAndBatchDelete(t *testing.T) {
	newHostTestDB(t)
	engine := gin.New()
	NewLineController(engine.Group("/panel/api"))

	create := func(name string) int {
		t.Helper()
		resp := doHostReq(t, engine, http.MethodPost, "/panel/api/lines", map[string]any{
			"type":         service.LineTypeCloudflare,
			"name":         name,
			"entryHost":    name + ".example.com",
			"entryPort":    8443,
			"outboundType": "socks5",
			"outboundHost": "res.example.net",
			"outboundPort": 1080,
			"config":       map[string]string{"wsPath": "/ws"},
		})
		if !resp.Success {
			t.Fatalf("create %s: %s", name, resp.Msg)
		}
		var line service.LineDetail
		if err := json.Unmarshal(resp.Obj, &line); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		return line.Id
	}

	firstID := create("first")
	secondID := create("second")
	apply := doHostReq(t, engine, http.MethodPost, "/panel/api/lines/1/apply", map[string]any{})
	if !apply.Success {
		t.Fatalf("apply first: %s", apply.Msg)
	}

	deleted := doHostReq(t, engine, http.MethodPost, "/panel/api/lines/1/delete", map[string]any{})
	if !deleted.Success {
		t.Fatalf("delete first: %s", deleted.Msg)
	}
	if err := database.GetDB().First(&model.LineProfile{}, firstID).Error; err == nil {
		t.Fatal("deleted line still exists")
	}
	var inboundCount int64
	if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "line-1-in").Count(&inboundCount).Error; err != nil || inboundCount != 0 {
		t.Fatalf("deleted inbound count = %d, err=%v", inboundCount, err)
	}
	var template model.Setting
	if err := database.GetDB().Where("key = ?", "xrayTemplateConfig").First(&template).Error; err != nil {
		t.Fatalf("load template: %v", err)
	}
	if strings.Contains(template.Value, "line-1-in") || strings.Contains(template.Value, "line-1-out") {
		t.Fatalf("deleted line artifacts remain in template: %s", template.Value)
	}
	var managedClientCount int64
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", "line-1-user").Count(&managedClientCount).Error; err != nil || managedClientCount != 0 {
		t.Fatalf("deleted line client count = %d, err=%v", managedClientCount, err)
	}

	batch := doHostReq(t, engine, http.MethodPost, "/panel/api/lines/batch-delete", map[string]any{"ids": []int{secondID}})
	if !batch.Success {
		t.Fatalf("batch delete: %s", batch.Msg)
	}
	if err := database.GetDB().First(&model.LineProfile{}, secondID).Error; err == nil {
		t.Fatal("batch deleted line still exists")
	}
}

func assertXrayTemplateHasLineRoute(t *testing.T, raw string, inboundTag string, outboundTag string) {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("decode xray template: %v", err)
	}
	outbounds, _ := cfg["outbounds"].([]any)
	hasOutbound := false
	for _, item := range outbounds {
		m, _ := item.(map[string]any)
		if tag, _ := m["tag"].(string); tag == outboundTag {
			hasOutbound = true
			break
		}
	}
	routing, _ := cfg["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	hasRoute := false
	for _, item := range rules {
		m, _ := item.(map[string]any)
		if out, _ := m["outboundTag"].(string); out != outboundTag {
			continue
		}
		tags, _ := m["inboundTag"].([]any)
		if len(tags) == 1 && tags[0] == inboundTag {
			hasRoute = true
			break
		}
	}
	if !hasOutbound || !hasRoute {
		t.Fatalf("xray template missing line outbound/routing: %s", raw)
	}
}
