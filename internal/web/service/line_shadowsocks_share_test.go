package service

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestShadowsocks2022ShareCredentials(t *testing.T) {
	const serverKey = "AQIDBAUGBwgJCgsMDQ4PEA=="
	const clientKey = "ERITFBUWFxgZGhscHR4fIA=="
	inbound := &model.Inbound{Settings: `{"method":"2022-blake3-aes-128-gcm","password":"` + serverKey + `"}`}

	credentials, err := shadowsocks2022ShareCredentials(inbound, clientKey)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if got, want := credentials.combinedPassword(), serverKey+":"+clientKey; got != want {
		t.Fatalf("combined password = %q, want %q", got, want)
	}

	link := buildShadowsocksShareLink(model.LineProfile{Name: "SS direct", EntryHost: "ss.example.test", EntryPort: 30080}, credentials)
	if !strings.HasPrefix(link, "ss://2022-blake3-aes-128-gcm:") || strings.Contains(link, "ss://MjAyMi") {
		t.Fatalf("SS2022 link must use plain SIP022 userinfo, got %q", link)
	}
	if !strings.Contains(link, "@ss.example.test:30080#SS+direct") {
		t.Fatalf("SS2022 link endpoint or remark is wrong: %q", link)
	}
}

func TestShadowsocks2022ShareCredentialsRejectsMissingServerKey(t *testing.T) {
	inbound := &model.Inbound{Settings: `{"method":"2022-blake3-aes-128-gcm"}`}
	if _, err := shadowsocks2022ShareCredentials(inbound, "ERITFBUWFxgZGhscHR4fIA=="); err == nil {
		t.Fatal("missing server key must not produce a share credential")
	}
}
