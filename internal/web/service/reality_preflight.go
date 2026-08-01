package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const (
	realityPreflightReadyTimeout = 5 * time.Second
	realityPreflightHTTPTimeout  = 15 * time.Second
	realityPreflightURL          = "https://www.google.com/generate_204"
)

// realityApplyPreflight is deliberately built before the line transaction.
// The candidate is only copied into the persisted line after this isolated
// Xray server/client round trip succeeds.
type realityApplyPreflight struct {
	config                  map[string]string
	detail                  string
	sourceConfigJSON        string
	sourceLineUpdatedAt     int64
	sourceOutboundUpdatedAt int64
}

func (s *LineService) prepareRealityApply(id int) (*realityApplyPreflight, error) {
	var line model.LineProfile
	if err := database.GetDB().First(&line, id).Error; err != nil {
		return nil, err
	}
	var outbound model.LineOutbound
	if err := database.GetDB().Where("line_id = ?", id).Order("id asc").First(&outbound).Error; err != nil {
		return nil, err
	}

	cfg := ensureLineConfigDefaults(line.Id, line.Type, decodeLineConfig(line.ConfigJSON))
	if err := ensureRealityConfig(cfg); err != nil {
		return nil, err
	}
	if err := validateRealityApplyInputs(&line, &outbound, cfg, false); err != nil {
		return nil, err
	}

	if isRealityAutoTarget(cfg) {
		results, err := (&ServerService{}).ScanRealityTargets("")
		if err != nil {
			return nil, fmt.Errorf("scan Reality candidates: %w", err)
		}
		var failures []string
		for _, result := range results {
			if !result.Feasible {
				continue
			}
			candidate := cloneLineConfig(cfg)
			candidate["realitySni"] = result.Host
			candidate["realityDest"] = net.JoinHostPort(result.Host, strconv.Itoa(result.Port))
			if err := runRealityPreflight(&line, &outbound, candidate); err == nil {
				return &realityApplyPreflight{
					config:                  candidate,
					detail:                  fmt.Sprintf("automatic target %s passed %d/%d TLS probes and the isolated Reality check", result.Target, result.Successes, result.Rounds),
					sourceConfigJSON:        line.ConfigJSON,
					sourceLineUpdatedAt:     line.UpdatedAt,
					sourceOutboundUpdatedAt: outbound.UpdatedAt,
				}, nil
			} else {
				failures = append(failures, result.Target+": "+err.Error())
			}
		}
		if len(failures) == 0 {
			return nil, fmt.Errorf("no stable Reality target passed the TLS scan")
		}
		return nil, fmt.Errorf("no stable Reality target passed the real connection check; first failure: %s", failures[0])
	}

	if err := validateRealityApplyInputs(&line, &outbound, cfg, true); err != nil {
		return nil, err
	}
	if err := runRealityPreflight(&line, &outbound, cfg); err != nil {
		return nil, fmt.Errorf("Reality real connection check failed: %w", err)
	}
	return &realityApplyPreflight{
		config:                  cfg,
		detail:                  "manual target passed the isolated Reality check",
		sourceConfigJSON:        line.ConfigJSON,
		sourceLineUpdatedAt:     line.UpdatedAt,
		sourceOutboundUpdatedAt: outbound.UpdatedAt,
	}, nil
}

func validateRealityApplyInputs(line *model.LineProfile, outbound *model.LineOutbound, cfg map[string]string, requireTarget bool) error {
	if line == nil || outbound == nil {
		return fmt.Errorf("line and residential outbound are required")
	}
	if err := validateHostValue("entry host", line.EntryHost); err != nil {
		return err
	}
	if line.EntryPort < 1 || line.EntryPort > 65535 {
		return fmt.Errorf("entry port is invalid: %d", line.EntryPort)
	}
	if strings.TrimSpace(outbound.Host) == "" || outbound.Port < 1 || outbound.Port > 65535 {
		return fmt.Errorf("residential outbound host and port are required")
	}
	if requireTarget {
		return validateRealityConfig(cfg)
	}
	return nil
}

func isRealityAutoTarget(cfg map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(cfg["realitySni"]), "auto") ||
		strings.EqualFold(strings.TrimSpace(cfg["realityDest"]), "auto")
}

func cloneLineConfig(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// runRealityPreflight uses a short-lived Xray whose server and client are both
// loopback-only. The server's Reality target and residential outbound are the
// exact settings that would be persisted for the line, while the client uses
// the same UUID, public key, short ID, fingerprint, and Vision flow as the
// generated subscription. It therefore catches a target that passes ordinary
// TLS but fails a real Reality handshake, without touching the running core.
func runRealityPreflight(line *model.LineProfile, outbound *model.LineOutbound, cfg map[string]string) error {
	ports, release, err := reserveRealityPreflightPorts(2)
	if err != nil {
		return fmt.Errorf("reserve preflight ports: %w", err)
	}
	defer release()

	testConfigPath, err := createRealityPreflightConfigPath()
	if err != nil {
		return fmt.Errorf("create preflight config: %w", err)
	}
	defer os.Remove(testConfigPath)

	proc := xray.NewTestProcess(buildRealityPreflightConfig(line, outbound, cfg, ports[0], ports[1]), testConfigPath)
	defer func() {
		if proc.IsRunning() {
			_ = proc.Stop()
		}
	}()

	release()
	if err := proc.Start(); err != nil {
		return fmt.Errorf("start temporary Xray: %w", err)
	}
	if err := waitForRealityPreflightPort(proc, ports[1]); err != nil {
		return err
	}
	if err := probeRealityPreflightSocks(ports[1]); err != nil {
		return err
	}
	if !proc.IsRunning() {
		return fmt.Errorf("temporary Xray exited: %s", proc.GetResult())
	}
	return nil
}

func buildRealityPreflightConfig(line *model.LineProfile, outbound *model.LineOutbound, cfg map[string]string, serverPort int, socksPort int) *xray.Config {
	serverInbound := buildRealityInbound(line, "reality-preflight-server", cfg)
	clientID := strings.TrimSpace(cfg["clientId"])
	flow := strings.TrimSpace(cfg["realityFlow"])
	fingerprint := strings.TrimSpace(cfg["realityFingerprint"])
	spiderX := strings.TrimSpace(cfg["realitySpiderX"])

	clientOutbound := map[string]any{
		"tag":      "reality-preflight-client",
		"protocol": "vless",
		"settings": map[string]any{
			"vnext": []map[string]any{{
				"address": "127.0.0.1",
				"port":    serverPort,
				"users": []map[string]any{{
					"id":         clientID,
					"encryption": "none",
					"flow":       flow,
				}},
			}},
		},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"show":        false,
				"fingerprint": fingerprint,
				"serverName":  strings.TrimSpace(cfg["realitySni"]),
				"publicKey":   strings.TrimSpace(cfg["realityPublicKey"]),
				"shortId":     strings.TrimSpace(cfg["realityShortId"]),
				"spiderX":     spiderX,
			},
		},
	}
	residential := buildResidentialOutboundConfig("reality-preflight-residential", outbound)
	outboundsJSON, _ := json.Marshal([]any{
		clientOutbound,
		residential,
		map[string]any{"tag": "direct", "protocol": "freedom"},
	})
	routingJSON, _ := json.Marshal(map[string]any{
		"domainStrategy": "AsIs",
		"rules": []map[string]any{
			{"type": "field", "inboundTag": []string{"reality-preflight-socks"}, "outboundTag": "reality-preflight-client"},
			{"type": "field", "inboundTag": []string{"reality-preflight-server"}, "outboundTag": "reality-preflight-residential"},
		},
	})
	logJSON, _ := json.Marshal(map[string]any{"loglevel": "warning", "access": "none", "error": "", "dnsLog": false})

	return &xray.Config{
		LogConfig: json_util.RawMessage(logJSON),
		InboundConfigs: []xray.InboundConfig{
			{
				Listen:         json_util.RawMessage(`"127.0.0.1"`),
				Port:           serverPort,
				Protocol:       string(serverInbound.Protocol),
				Settings:       json_util.RawMessage(serverInbound.Settings),
				StreamSettings: json_util.RawMessage(serverInbound.StreamSettings),
				Tag:            "reality-preflight-server",
				Sniffing:       json_util.RawMessage(serverInbound.Sniffing),
			},
			{
				Listen:   json_util.RawMessage(`"127.0.0.1"`),
				Port:     socksPort,
				Protocol: "socks",
				Settings: json_util.RawMessage(`{"auth":"noauth","udp":false}`),
				Tag:      "reality-preflight-socks",
			},
		},
		OutboundConfigs: json_util.RawMessage(outboundsJSON),
		RouterConfig:    json_util.RawMessage(routingJSON),
		Policy:          json_util.RawMessage(`{}`),
		Stats:           json_util.RawMessage(`{}`),
	}
}

func reserveRealityPreflightPorts(n int) ([]int, func(), error) {
	listeners := make([]net.Listener, 0, n)
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}
	ports := make([]int, 0, n)
	for range n {
		listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			release()
			return nil, nil, err
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	return ports, release, nil
}

func createRealityPreflightConfigPath() (string, error) {
	file, err := os.CreateTemp(config.GetBinFolderPath(), "reality_preflight_*.json")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func waitForRealityPreflightPort(proc *xray.Process, port int) error {
	deadline := time.Now().Add(realityPreflightReadyTimeout)
	for {
		if !proc.IsRunning() {
			return fmt.Errorf("temporary Xray exited before it opened the test client: %s", proc.GetResult())
		}
		conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(context.Background(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("temporary Xray did not open the test client in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func probeRealityPreflightSocks(port int) error {
	proxyURL := &url.URL{Scheme: "socks5", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: realityPreflightHTTPTimeout}
	response, err := client.Get(realityPreflightURL)
	if err != nil {
		return fmt.Errorf("request through Reality and residential outbound failed: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	return nil
}
