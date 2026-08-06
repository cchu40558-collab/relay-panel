package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	cryptoutil "github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
)

var errLineSubscriptionUnavailable = errors.New("line subscription unavailable")

// LineClashSubscriptionShare contains only the address intended for an
// administrator. The token itself is never part of line detail/list payloads.
type LineClashSubscriptionShare struct {
	URL       string `json:"url"`
	CreatedAt int64  `json:"createdAt"`
	RotatedAt int64  `json:"rotatedAt"`
}

func (s *LineService) GetLineClashSubscriptionShare(id int) (*LineClashSubscriptionShare, error) {
	line, _, _, inbound, err := loadLineRuntime(id)
	if err != nil {
		return nil, err
	}
	if err := validateLineCanApply(line, time.Now()); err != nil {
		return nil, err
	}
	if inbound == nil {
		return nil, fmt.Errorf("line has not been applied yet")
	}
	if line.Type != LineTypeCloudflare && line.Type != LineTypeReality && line.Type != LineTypeShadowsocks {
		return nil, fmt.Errorf("Clash subscription is not available for this line type")
	}

	hostLine, hostConfig, err := findLineSubscriptionHost()
	if err != nil {
		return nil, err
	}
	if !truthy(hostConfig["nginxApply"]) {
		return nil, fmt.Errorf("Clash subscription requires an applied Cloudflare line with Nginx write enabled")
	}
	if _, err := applyCloudflareNginx(hostLine, hostConfig); err != nil {
		return nil, fmt.Errorf("configure subscription Nginx route: %w", err)
	}

	token, record, err := getOrCreateLineSubscriptionToken(line.Id, false)
	if err != nil {
		return nil, err
	}
	url, err := buildLineSubscriptionURL(hostLine, token)
	if err != nil {
		return nil, err
	}
	return &LineClashSubscriptionShare{URL: url, CreatedAt: record.CreatedAt, RotatedAt: record.RotatedAt}, nil
}

func (s *LineService) ResetLineClashSubscription(id int) (*LineClashSubscriptionShare, error) {
	line, _, _, inbound, err := loadLineRuntime(id)
	if err != nil {
		return nil, err
	}
	if err := validateLineCanApply(line, time.Now()); err != nil {
		return nil, err
	}
	if inbound == nil {
		return nil, fmt.Errorf("line has not been applied yet")
	}
	if line.Type != LineTypeCloudflare && line.Type != LineTypeReality && line.Type != LineTypeShadowsocks {
		return nil, fmt.Errorf("Clash subscription is not available for this line type")
	}

	hostLine, hostConfig, err := findLineSubscriptionHost()
	if err != nil {
		return nil, err
	}
	if !truthy(hostConfig["nginxApply"]) {
		return nil, fmt.Errorf("Clash subscription requires an applied Cloudflare line with Nginx write enabled")
	}
	if _, err := applyCloudflareNginx(hostLine, hostConfig); err != nil {
		return nil, fmt.Errorf("configure subscription Nginx route: %w", err)
	}

	token, record, err := getOrCreateLineSubscriptionToken(line.Id, true)
	if err != nil {
		return nil, err
	}
	url, err := buildLineSubscriptionURL(hostLine, token)
	if err != nil {
		return nil, err
	}
	return &LineClashSubscriptionShare{URL: url, CreatedAt: record.CreatedAt, RotatedAt: record.RotatedAt}, nil
}

func (s *LineService) GetLineClashSubscriptionYAML(id int) ([]byte, string, error) {
	if _, err := s.GetLineClashSubscriptionShare(id); err != nil {
		return nil, "", err
	}
	return buildLineClashSubscriptionYAML(id)
}

// GetPublicLineClashSubscription validates only the opaque token and returns a
// YAML profile. Callers deliberately receive no reason when it is unavailable.
func (s *LineService) GetPublicLineClashSubscription(token string) ([]byte, string, error) {
	token = strings.TrimSpace(token)
	if len(token) < 32 || len(token) > 256 {
		return nil, "", errLineSubscriptionUnavailable
	}
	var subscription model.LineSubscription
	if err := database.GetDB().Where("token_hash = ?", cryptoutil.HashTokenSHA256(token)).First(&subscription).Error; err != nil {
		return nil, "", errLineSubscriptionUnavailable
	}
	body, filename, err := buildLineClashSubscriptionYAML(subscription.LineId)
	if err != nil {
		return nil, "", errLineSubscriptionUnavailable
	}
	return body, filename, nil
}

func buildLineClashSubscriptionYAML(id int) ([]byte, string, error) {
	line, _, config, inbound, err := loadLineRuntime(id)
	if err != nil {
		return nil, "", err
	}
	if err := validateLineCanApply(line, time.Now()); err != nil {
		return nil, "", err
	}
	if inbound == nil {
		return nil, "", fmt.Errorf("line has not been applied yet")
	}
	proxy, err := buildLineClashProxy(line, inbound, config)
	if err != nil {
		return nil, "", err
	}
	proxyName := line.Name
	if strings.TrimSpace(proxyName) == "" {
		proxyName = lineTypeName(line.Type)
	}
	document := struct {
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []map[string]any `yaml:"proxy-groups"`
		Rules       []string         `yaml:"rules"`
	}{
		Proxies: []map[string]any{proxy},
		ProxyGroups: []map[string]any{{
			"name":    "Relay",
			"type":    "select",
			"proxies": []string{proxyName, "DIRECT"},
		}},
		Rules: []string{"MATCH,Relay"},
	}
	body, err := yaml.Marshal(document)
	if err != nil {
		return nil, "", fmt.Errorf("encode Clash YAML: %w", err)
	}
	return body, fmt.Sprintf("relay-panel-line-%d.yaml", line.Id), nil
}

func buildLineClashProxy(line model.LineProfile, inbound *model.Inbound, config map[string]string) (map[string]any, error) {
	if inbound == nil {
		return nil, fmt.Errorf("line has not been applied yet")
	}
	host := strings.TrimSpace(line.EntryHost)
	if host == "" {
		return nil, fmt.Errorf("entry host is required")
	}
	name := strings.TrimSpace(line.Name)
	if name == "" {
		name = lineTypeName(line.Type)
	}
	switch line.Type {
	case LineTypeCloudflare:
		clientID, err := firstVlessClientID(inbound.Settings)
		if err != nil {
			return nil, err
		}
		proxy := map[string]any{
			"name":               name,
			"type":               "vless",
			"server":             strings.Trim(host, "[]"),
			"port":               line.EntryPort,
			"uuid":               clientID,
			"udp":                true,
			"tls":                true,
			"client-fingerprint": "chrome",
		}
		wsPath := strings.TrimSpace(config["wsPath"])
		if wsPath == "" {
			wsPath = "/"
		}
		proxy["servername"] = host
		proxy["network"] = "ws"
		proxy["ws-opts"] = map[string]any{
			"path":    wsPath,
			"headers": map[string]string{"Host": host},
		}
		return proxy, nil
	case LineTypeReality:
		clientID, err := firstVlessClientID(inbound.Settings)
		if err != nil {
			return nil, err
		}
		proxy := map[string]any{
			"name":               name,
			"type":               "vless",
			"server":             strings.Trim(host, "[]"),
			"port":               line.EntryPort,
			"uuid":               clientID,
			"udp":                true,
			"tls":                true,
			"client-fingerprint": "chrome",
		}
		publicKey := strings.TrimSpace(config["realityPublicKey"])
		shortID := strings.TrimSpace(config["realityShortId"])
		sni := strings.TrimSpace(config["realitySni"])
		if publicKey == "" || shortID == "" || sni == "" {
			return nil, fmt.Errorf("Reality subscription settings are incomplete")
		}
		fingerprint := strings.TrimSpace(config["realityFingerprint"])
		if fingerprint == "" {
			fingerprint = "chrome"
		}
		flow := strings.TrimSpace(config["realityFlow"])
		if flow == "" {
			flow = "xtls-rprx-vision"
		}
		proxy["servername"] = sni
		proxy["client-fingerprint"] = fingerprint
		proxy["network"] = "tcp"
		proxy["flow"] = flow
		proxy["reality-opts"] = map[string]string{
			"public-key": publicKey,
			"short-id":   shortID,
		}
		return proxy, nil
	case LineTypeShadowsocks:
		clientKey, err := lineManagedShadowsocksPassword(line.Id)
		if err != nil {
			return nil, err
		}
		credentials, err := shadowsocks2022ShareCredentials(inbound, clientKey)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"name":     name,
			"type":     "ss",
			"server":   strings.Trim(host, "[]"),
			"port":     line.EntryPort,
			"cipher":   credentials.method,
			"password": credentials.combinedPassword(),
			"udp":      false,
		}, nil
	default:
		return nil, fmt.Errorf("Clash subscription is not available for this line type")
	}
}

func findLineSubscriptionHost() (model.LineProfile, map[string]string, error) {
	var candidates []model.LineProfile
	if err := database.GetDB().Where("type = ? AND inbound_id IS NOT NULL", LineTypeCloudflare).Order("id asc").Find(&candidates).Error; err != nil {
		return model.LineProfile{}, nil, err
	}
	for _, line := range candidates {
		if err := validateLineCanApply(line, time.Now()); err != nil {
			continue
		}
		config := ensureLineConfigDefaults(line.Id, line.Type, decodeLineConfig(line.ConfigJSON))
		if strings.TrimSpace(line.EntryHost) == "" || line.EntryPort <= 0 {
			continue
		}
		return line, config, nil
	}
	return model.LineProfile{}, nil, fmt.Errorf("no applied Cloudflare main line is available for Clash subscription delivery")
}

func getOrCreateLineSubscriptionToken(lineID int, rotate bool) (string, model.LineSubscription, error) {
	var current model.LineSubscription
	err := database.GetDB().Where("line_id = ?", lineID).First(&current).Error
	if err == nil && !rotate {
		token, err := decryptLineSubscriptionToken(current.TokenCiphertext)
		return token, current, err
	}
	notFound := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !notFound {
		return "", model.LineSubscription{}, err
	}

	token, ciphertext, err := newLineSubscriptionToken()
	if err != nil {
		return "", model.LineSubscription{}, err
	}
	now := time.Now().Unix()
	next := model.LineSubscription{
		LineId:          lineID,
		TokenHash:       cryptoutil.HashTokenSHA256(token),
		TokenCiphertext: ciphertext,
		RotatedAt:       now,
	}
	if notFound {
		next.RotatedAt = 0
		if err := database.GetDB().Create(&next).Error; err != nil {
			return "", model.LineSubscription{}, err
		}
		return token, next, nil
	}
	if err := database.GetDB().Model(&current).Updates(map[string]any{
		"token_hash":       next.TokenHash,
		"token_ciphertext": next.TokenCiphertext,
		"rotated_at":       now,
	}).Error; err != nil {
		return "", model.LineSubscription{}, err
	}
	current.TokenHash = next.TokenHash
	current.TokenCiphertext = next.TokenCiphertext
	current.RotatedAt = now
	return token, current, nil
}

func newLineSubscriptionToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate subscription token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	ciphertext, err := encryptLineSubscriptionToken(token)
	if err != nil {
		return "", "", err
	}
	return token, ciphertext, nil
}

func lineSubscriptionKey() ([]byte, error) {
	secret, err := (&SettingService{}).GetSecret()
	if err != nil {
		return nil, err
	}
	if len(secret) == 0 {
		return nil, errors.New("panel secret is empty")
	}
	sum := sha256.Sum256(secret)
	return sum[:], nil
}

func encryptLineSubscriptionToken(token string) (string, error) {
	key, err := lineSubscriptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := gcm.Seal(nonce, nonce, []byte(token), nil)
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decryptLineSubscriptionToken(ciphertext string) (string, error) {
	key, err := lineSubscriptionKey()
	if err != nil {
		return "", err
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted subscription token")
	}
	plain, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func lineSubscriptionPath() string {
	basePath, err := (&SettingService{}).GetBasePath()
	if err != nil || strings.TrimSpace(basePath) == "" {
		basePath = "/"
	}
	return strings.TrimRight(basePath, "/") + "/rp/sub/"
}

func buildLineSubscriptionURL(hostLine model.LineProfile, token string) (string, error) {
	host := strings.TrimSpace(hostLine.EntryHost)
	if host == "" || hostLine.EntryPort <= 0 || hostLine.EntryPort > 65535 {
		return "", errors.New("Cloudflare subscription host is incomplete")
	}
	authority := formatShareHost(host)
	if hostLine.EntryPort != 443 {
		authority = fmt.Sprintf("%s:%d", authority, hostLine.EntryPort)
	}
	return "https://" + authority + lineSubscriptionPath() + token, nil
}

// buildLineSubscriptionNginxLocation is included in every managed Cloudflare
// server block so a later re-apply never removes the subscription endpoint.
func buildLineSubscriptionNginxLocation() string {
	settings := &SettingService{}
	panelPort, err := settings.GetPort()
	if err != nil || panelPort <= 0 || panelPort > 65535 {
		panelPort = 2053
	}
	scheme := "http"
	proxyTLS := ""
	if certFile, certErr := settings.GetCertFile(); certErr == nil && strings.TrimSpace(certFile) != "" {
		if keyFile, keyErr := settings.GetKeyFile(); keyErr == nil && strings.TrimSpace(keyFile) != "" {
			scheme = "https"
			proxyTLS = "\n        proxy_ssl_verify off;"
		}
	}
	return fmt.Sprintf(`    location ^~ %s {
        access_log off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;%s
        proxy_pass %s://127.0.0.1:%d;
    }`, lineSubscriptionPath(), proxyTLS, scheme, panelPort)
}
