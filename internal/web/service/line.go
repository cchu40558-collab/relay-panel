package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const (
	LineTypeCloudflare = "cloudflare_ws_tls"
	LineTypeReality    = "reality_direct"
	LineTypeTrojan     = "trojan_direct"
)

var managedNginxCertRoot = "/etc/line-panel/nginx-certs"

type LineService struct{}

type LineSaveRequest struct {
	Type             string            `json:"type"`
	Name             string            `json:"name"`
	EntryHost        string            `json:"entryHost"`
	EntryPort        int               `json:"entryPort"`
	OutboundType     string            `json:"outboundType"`
	OutboundHost     string            `json:"outboundHost"`
	OutboundPort     int               `json:"outboundPort"`
	OutboundUsername string            `json:"outboundUsername"`
	OutboundPassword string            `json:"outboundPassword"`
	Config           map[string]string `json:"config"`
}

type LineDetail struct {
	model.LineProfile
	Outbound *model.LineOutbound  `json:"outbound,omitempty"`
	Config   map[string]string    `json:"config"`
	Plan     *LineApplyPlan       `json:"plan,omitempty"`
	Logs     []model.LineApplyLog `json:"logs"`
}

type LineApplyPlan struct {
	Title        string         `json:"title"`
	Summary      []string       `json:"summary"`
	XrayInbound  map[string]any `json:"xrayInbound"`
	XrayOutbound map[string]any `json:"xrayOutbound"`
	Nginx        string         `json:"nginx,omitempty"`
	Checks       []string       `json:"checks"`
}

type LineCheckResponse struct {
	Status    string          `json:"status"`
	PassCount int             `json:"passCount"`
	WarnCount int             `json:"warnCount"`
	FailCount int             `json:"failCount"`
	Items     []LineCheckItem `json:"items"`
}

type LineCheckItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type LineDeleteResult struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type LineShareResponse struct {
	Links []LineShareLink `json:"links"`
}

type LineShareLink struct {
	Label string `json:"label"`
	URI   string `json:"uri"`
}

// OriginCertificateUploadResponse deliberately exposes only non-secret metadata.
type OriginCertificateUploadResponse struct {
	CertificateFile string    `json:"certificateFile"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

func (s *LineService) GetLineTypes() []LineTypeInfo {
	return []LineTypeInfo{
		{
			Type:        LineTypeCloudflare,
			Name:        "Cloudflare 主线路",
			Description: "Cloudflare 橙云 -> Nginx -> VLESS WS TLS -> 住宅出口",
		},
		{
			Type:        LineTypeReality,
			Name:        "Reality 直连",
			Description: "用户 -> VPS Reality -> 住宅出口",
		},
	}
}

type LineTypeInfo struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *LineService) ListLines() ([]model.LineProfile, error) {
	var lines []model.LineProfile
	err := database.GetDB().Order("id desc").Find(&lines).Error
	return lines, err
}

func (s *LineService) GetLine(id int) (*LineDetail, error) {
	var line model.LineProfile
	if err := database.GetDB().First(&line, id).Error; err != nil {
		return nil, err
	}

	detail := &LineDetail{
		LineProfile: line,
		Config:      decodeLineConfig(line.ConfigJSON),
	}
	normalizedConfig := ensureLineConfigDefaults(line.Id, line.Type, detail.Config)
	if encodeLineConfig(normalizedConfig) != line.ConfigJSON {
		detail.Config = normalizedConfig
		detail.ConfigJSON = encodeLineConfig(normalizedConfig)
		_ = database.GetDB().Model(&model.LineProfile{}).Where("id = ?", line.Id).Update("config_json", detail.ConfigJSON).Error
	}

	var outbound model.LineOutbound
	err := database.GetDB().Where("line_id = ?", line.Id).Order("id asc").First(&outbound).Error
	if err == nil {
		detail.Outbound = &outbound
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	detail.Plan = buildLineApplyPlan(line, detail.Outbound, detail.Config)
	if err := database.GetDB().Where("line_id = ?", line.Id).Order("id desc").Limit(20).Find(&detail.Logs).Error; err != nil {
		return nil, err
	}

	return detail, nil
}

// StageCloudflareOriginCertificate validates an uploaded origin certificate pair and
// stores it outside the database. It becomes active only during a later ApplyLine.
func (s *LineService) StageCloudflareOriginCertificate(id int, certificatePEM, privateKeyPEM []byte) (*OriginCertificateUploadResponse, error) {
	if id <= 0 {
		return nil, fmt.Errorf("line ID is required")
	}
	expiresAt, err := validateOriginCertificatePair(certificatePEM, privateKeyPEM, time.Now())
	if err != nil {
		return nil, err
	}

	var line model.LineProfile
	if err := database.GetDB().First(&line, id).Error; err != nil {
		return nil, err
	}
	if line.Type != LineTypeCloudflare {
		return nil, fmt.Errorf("origin certificates are supported only for Cloudflare lines")
	}

	certificateFile, keyFile, err := writeManagedOriginCertificate(id, certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, err
	}
	config := ensureLineConfigDefaults(line.Id, line.Type, decodeLineConfig(line.ConfigJSON))
	config["nginxPendingCertFile"] = certificateFile
	config["nginxPendingKeyFile"] = keyFile
	config["nginxPendingCertExpiresAt"] = expiresAt.UTC().Format(time.RFC3339)
	config["nginxCertMode"] = "managed"
	if err := database.GetDB().Model(&line).Update("config_json", encodeLineConfig(config)).Error; err != nil {
		_ = os.RemoveAll(filepath.Dir(certificateFile))
		return nil, err
	}
	return &OriginCertificateUploadResponse{CertificateFile: certificateFile, ExpiresAt: expiresAt.UTC()}, nil
}

func validateOriginCertificatePair(certificatePEM, privateKeyPEM []byte, now time.Time) (time.Time, error) {
	certificateBlock, _ := pem.Decode(certificatePEM)
	if certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" {
		return time.Time{}, fmt.Errorf("origin certificate must be PEM encoded")
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse origin certificate: %w", err)
	}
	if now.Before(certificate.NotBefore) {
		return time.Time{}, fmt.Errorf("origin certificate is not valid yet")
	}
	if !now.Before(certificate.NotAfter) {
		return time.Time{}, fmt.Errorf("origin certificate has expired")
	}

	keyBlock, _ := pem.Decode(privateKeyPEM)
	if keyBlock == nil {
		return time.Time{}, fmt.Errorf("origin private key must be PEM encoded")
	}
	signer, err := parseOriginPrivateKey(keyBlock)
	if err != nil {
		return time.Time{}, err
	}
	certificatePublicKey, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil {
		return time.Time{}, fmt.Errorf("encode origin certificate public key: %w", err)
	}
	privatePublicKey, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return time.Time{}, fmt.Errorf("encode origin private key public key: %w", err)
	}
	if !bytes.Equal(certificatePublicKey, privatePublicKey) {
		return time.Time{}, fmt.Errorf("origin certificate does not match the private key")
	}
	return certificate.NotAfter, nil
}

func parseOriginPrivateKey(block *pem.Block) (crypto.Signer, error) {
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if signer, ok := key.(crypto.Signer); ok {
			return signer, nil
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("origin private key must be an unencrypted RSA, EC, or PKCS#8 PEM key")
}

func managedNginxCertLineDir(lineID int) string {
	return filepath.Join(managedNginxCertRoot, fmt.Sprintf("line-%d", lineID))
}

func writeManagedOriginCertificate(lineID int, certificatePEM, privateKeyPEM []byte) (string, string, error) {
	lineDir := managedNginxCertLineDir(lineID)
	versionDir := filepath.Join(lineDir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), uuid.NewString()))
	if err := os.MkdirAll(versionDir, 0700); err != nil {
		return "", "", fmt.Errorf("create managed certificate directory: %w", err)
	}
	for _, dir := range []string{managedNginxCertRoot, lineDir, versionDir} {
		if err := os.Chmod(dir, 0700); err != nil {
			_ = os.RemoveAll(versionDir)
			return "", "", fmt.Errorf("secure managed certificate directory: %w", err)
		}
	}
	certificateFile := filepath.Join(versionDir, "origin.crt")
	keyFile := filepath.Join(versionDir, "origin.key")
	if err := writePrivateFileAtomically(certificateFile, certificatePEM, 0644); err != nil {
		_ = os.RemoveAll(versionDir)
		return "", "", err
	}
	if err := writePrivateFileAtomically(keyFile, privateKeyPEM, 0600); err != nil {
		_ = os.RemoveAll(versionDir)
		return "", "", err
	}
	return certificateFile, keyFile, nil
}

func writePrivateFileAtomically(path string, content []byte, permission os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return fmt.Errorf("create certificate file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write certificate file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync certificate file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close certificate file: %w", err)
	}
	if err := os.Chmod(temporaryPath, permission); err != nil {
		return fmt.Errorf("set certificate file permissions: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("activate certificate file: %w", err)
	}
	return nil
}

func (s *LineService) CreateLine(req LineSaveRequest) (*LineDetail, error) {
	normalized, err := normalizeLineSaveRequest(req, 0)
	if err != nil {
		return nil, err
	}

	var createdID int
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		line := &model.LineProfile{
			UserId:      1,
			Name:        normalized.Name,
			Type:        normalized.Type,
			Status:      "pending_apply",
			EntryHost:   normalized.EntryHost,
			EntryPort:   normalized.EntryPort,
			ChainText:   buildLineChainText(normalized.Type, normalized.OutboundType),
			ConfigJSON:  encodeLineConfig(normalized.Config),
			OutboundTag: "",
		}
		if err := tx.Create(line).Error; err != nil {
			return err
		}

		normalized.Config = ensureLineConfigDefaults(line.Id, normalized.Type, normalized.Config)
		if err := tx.Model(line).Update("config_json", encodeLineConfig(normalized.Config)).Error; err != nil {
			return err
		}

		tag := fmt.Sprintf("line-%d-out", line.Id)
		outbound := lineOutboundFromRequest(line.Id, tag, normalized, "")
		if err := tx.Create(outbound).Error; err != nil {
			return err
		}
		if err := tx.Model(line).Update("outbound_tag", tag).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.LineApplyLog{
			LineId:  line.Id,
			Action:  "plan",
			Level:   "info",
			Message: "已生成部署计划，等待接入真实执行器",
			Detail:  buildLineChainText(normalized.Type, normalized.OutboundType),
		}).Error; err != nil {
			return err
		}

		createdID = line.Id
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetLine(createdID)
}

func (s *LineService) UpdateLine(id int, req LineSaveRequest) (*LineDetail, error) {
	normalized, err := normalizeLineSaveRequest(req, id)
	if err != nil {
		return nil, err
	}

	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		var line model.LineProfile
		if err := tx.First(&line, id).Error; err != nil {
			return err
		}
		normalized.Config = mergePreservedLineConfig(decodeLineConfig(line.ConfigJSON), normalized.Config)
		normalized.Config = ensureLineConfigDefaults(line.Id, normalized.Type, normalized.Config)

		updates := map[string]any{
			"name":        normalized.Name,
			"type":        normalized.Type,
			"status":      "pending_apply",
			"entry_host":  normalized.EntryHost,
			"entry_port":  normalized.EntryPort,
			"chain_text":  buildLineChainText(normalized.Type, normalized.OutboundType),
			"config_json": encodeLineConfig(normalized.Config),
		}
		if err := tx.Model(&line).Updates(updates).Error; err != nil {
			return err
		}

		tag := line.OutboundTag
		if tag == "" {
			tag = fmt.Sprintf("line-%d-out", line.Id)
			if err := tx.Model(&line).Update("outbound_tag", tag).Error; err != nil {
				return err
			}
		}

		var outbound model.LineOutbound
		err := tx.Where("line_id = ?", id).Order("id asc").First(&outbound).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(lineOutboundFromRequest(id, tag, normalized, "")).Error; err != nil {
				return err
			}
			return tx.Create(&model.LineApplyLog{
				LineId:  id,
				Action:  "plan",
				Level:   "info",
				Message: "已更新部署计划，等待接入真实执行器",
				Detail:  buildLineChainText(normalized.Type, normalized.OutboundType),
			}).Error
		}
		if err != nil {
			return err
		}

		outbound.Type = normalized.OutboundType
		outbound.Host = normalized.OutboundHost
		outbound.Port = normalized.OutboundPort
		outbound.Username = normalized.OutboundUsername
		outbound.Tag = tag
		outbound.Enabled = true
		if normalized.OutboundPassword != "" {
			outbound.Password = normalized.OutboundPassword
		}
		if err := tx.Save(&outbound).Error; err != nil {
			return err
		}
		return tx.Create(&model.LineApplyLog{
			LineId:  id,
			Action:  "plan",
			Level:   "info",
			Message: "已更新部署计划，等待接入真实执行器",
			Detail:  buildLineChainText(normalized.Type, normalized.OutboundType),
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetLine(id)
}

func (s *LineService) ApplyLine(id int) (*LineDetail, error) {
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		var line model.LineProfile
		if err := tx.First(&line, id).Error; err != nil {
			return err
		}

		config := ensureLineConfigDefaults(line.Id, line.Type, decodeLineConfig(line.ConfigJSON))
		if line.Type == LineTypeCloudflare {
			if err := promotePendingOriginCertificate(config); err != nil {
				return wrapLineApplyFailure("validate", "Origin certificate validation failed", err)
			}
		}
		if err := tx.Model(&line).Updates(map[string]any{
			"status":      "applying",
			"last_error":  "",
			"config_json": encodeLineConfig(config),
		}).Error; err != nil {
			return err
		}

		var outbound model.LineOutbound
		err := tx.Where("line_id = ?", line.Id).Order("id asc").First(&outbound).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newLineApplyFailure("validate", "Missing residential outbound config", "Please fill residential outbound type, host and port")
			}
			return err
		}

		plan := buildLineApplyPlan(line, &outbound, config)
		if err := createLineLog(tx, line.Id, "apply", "info", "开始执行保存并应用", "执行器骨架已接收线路部署请求"); err != nil {
			return err
		}
		if err := validateLineForApply(&line, &outbound, config); err != nil {
			return wrapLineApplyFailure("validate", "Line apply validation failed", err)
		}
		if err := createLineLog(tx, line.Id, "validate", "info", "参数校验通过", strings.Join(plan.Summary, "\n")); err != nil {
			return err
		}
		if err := createLineLog(tx, line.Id, "xray", "info", "已生成 Xray 入站和出站草案", "下一阶段写入 3x-ui/Xray 配置并重启 Xray"); err != nil {
			return err
		}
		if line.Type == LineTypeCloudflare || line.Type == LineTypeReality {
			appliedInboundID, err := applyLineXray(tx, &line, &outbound, config)
			if err != nil {
				return wrapLineApplyFailure("xray", "Write Xray inbound/outbound failed", err)
			}
			line.InboundId = &appliedInboundID
			if err := createLineLog(tx, line.Id, "xray", "info", "已写入 Xray 入站和出站配置", fmt.Sprintf("inboundId=%d outboundTag=%s", appliedInboundID, line.OutboundTag)); err != nil {
				return err
			}
			if line.Type == LineTypeCloudflare {
				if err := createLineLog(tx, line.Id, "nginx", "info", "已生成 Nginx 草案", "下一阶段写入 Nginx 站点配置并 reload"); err != nil {
					return err
				}
				nginxDetail, err := applyCloudflareNginx(line, config)
				if err != nil {
					return wrapLineApplyFailure("nginx", "Nginx apply failed", err)
				}
				if nginxDetail != "" {
					if err := createLineLog(tx, line.Id, "nginx", "info", "Nginx 应用步骤完成", nginxDetail); err != nil {
						return err
					}
				}
			}
			return tx.Model(&line).Updates(map[string]any{
				"status":     "pending_check",
				"last_error": "",
				"inbound_id": appliedInboundID,
			}).Error
		}
		return newLineApplyFailure("executor", "Line type is not supported in MVP", fmt.Sprintf("line type %s is hidden until a later version", line.Type))
	})
	if err != nil {
		if failErr := recordLineApplyFailure(id, err); failErr != nil {
			return nil, fmt.Errorf("%w; record apply failure: %v", err, failErr)
		}
		detail, detailErr := s.GetLine(id)
		if detailErr != nil {
			return nil, err
		}
		return detail, err
	}
	return s.GetLine(id)
}

// DeleteLine removes only artifacts owned by a managed line: its tagged
// inbound, generated outbound, routing rule, and line-specific records.
func (s *LineService) DeleteLine(id int) (*LineDeleteResult, error) {
	var line model.LineProfile
	if err := database.GetDB().First(&line, id).Error; err != nil {
		return nil, err
	}

	result := &LineDeleteResult{ID: line.Id, Name: line.Name}
	config := decodeLineConfig(line.ConfigJSON)
	backup, err := removeLineNginxConfig(line, config)
	if err != nil {
		return result, err
	}

	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		inboundTag := fmt.Sprintf("line-%d-in", line.Id)
		if err := removeLineTemplateArtifacts(tx, line.OutboundTag, inboundTag); err != nil {
			return err
		}
		if err := removeLineManagedClient(tx, line.Id); err != nil {
			return err
		}
		if err := tx.Where("tag = ?", inboundTag).Delete(&model.Inbound{}).Error; err != nil {
			return err
		}
		if err := tx.Where("line_id = ?", line.Id).Delete(&model.LineCheckResult{}).Error; err != nil {
			return err
		}
		if err := tx.Where("line_id = ?", line.Id).Delete(&model.LineApplyLog{}).Error; err != nil {
			return err
		}
		if err := tx.Where("line_id = ?", line.Id).Delete(&model.LineOutbound{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.LineProfile{}, line.Id).Error
	})
	if err != nil {
		if restoreErr := restoreRemovedLineNginxConfig(backup); restoreErr != nil {
			return result, fmt.Errorf("delete line: %w; restore nginx: %v", err, restoreErr)
		}
		return result, err
	}
	result.Success = true
	if err := os.RemoveAll(managedNginxCertLineDir(line.Id)); err != nil {
		result.Message = fmt.Sprintf("line removed, but managed origin certificates could not be cleaned up: %v", err)
	}
	return result, nil
}

func (s *LineService) DeleteLines(ids []int) []LineDeleteResult {
	results := make([]LineDeleteResult, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result, err := s.DeleteLine(id)
		if err != nil {
			name := "线路"
			if result != nil && result.Name != "" {
				name = result.Name
			}
			results = append(results, LineDeleteResult{ID: id, Name: name, Message: err.Error()})
			continue
		}
		results = append(results, *result)
	}
	return results
}

func (s *LineService) CheckLine(id int) (*LineCheckResponse, error) {
	line, outbound, config, inbound, err := loadLineRuntime(id)
	if err != nil {
		return nil, err
	}

	items := make([]LineCheckItem, 0, 6)
	add := func(name string, status string, message string) {
		items = append(items, LineCheckItem{Name: name, Status: status, Message: message})
	}

	if strings.TrimSpace(line.EntryHost) == "" || line.EntryPort <= 0 {
		add("入口地址", "fail", "入口域名/IP 或端口未填写")
	} else {
		add("入口地址", "pass", formatHostPort(line.EntryHost, line.EntryPort))
	}

	if outbound == nil || strings.TrimSpace(outbound.Host) == "" || outbound.Port <= 0 {
		add("住宅出口", "fail", "住宅出口地址或端口未填写")
	} else {
		dialer := net.Dialer{Timeout: 3 * time.Second}
		conn, dialErr := dialer.Dial("tcp", net.JoinHostPort(outbound.Host, strconv.Itoa(outbound.Port)))
		if dialErr != nil {
			add("住宅出口连通性", "fail", dialErr.Error())
		} else {
			_ = conn.Close()
			add("住宅出口连通性", "pass", "TCP 端口可连接")
		}
	}

	if line.Type == LineTypeTrojan {
		add("执行器", "warn", "Trojan 直连执行器还没接入")
	} else {
		if inbound == nil {
			add("Xray 入站", "fail", "还没有生成 Xray 入站，请先保存并应用")
		} else if !inbound.Enable {
			add("Xray 入站", "fail", "Xray 入站已禁用")
		} else {
			add("Xray 入站", "pass", fmt.Sprintf("%s:%d %s", inbound.Listen, inbound.Port, inbound.Protocol))
		}

		outboundTag := line.OutboundTag
		inboundTag := fmt.Sprintf("line-%d-in", line.Id)
		if hasOutbound, hasRoute, templateErr := lineTemplateHasRoute(outboundTag, inboundTag); templateErr != nil {
			add("Xray 路由", "fail", templateErr.Error())
		} else if !hasOutbound || !hasRoute {
			add("Xray 路由", "fail", "缺少线路出站或入站到出站的路由规则")
		} else {
			add("Xray 路由", "pass", inboundTag+" -> "+outboundTag)
		}

		if line.Type == LineTypeReality {
			if strings.TrimSpace(config["realityPublicKey"]) == "" || strings.TrimSpace(config["realityPrivateKey"]) == "" {
				add("Reality 密钥", "fail", "缺少 Reality public/private key，请重新保存并应用")
			} else {
				add("Reality 密钥", "pass", "已生成 public/private key")
			}
			if strings.TrimSpace(config["realitySni"]) == "" || strings.TrimSpace(config["realityShortId"]) == "" {
				add("Reality 参数", "fail", "缺少 SNI 或 Short ID")
			} else {
				add("Reality 参数", "pass", config["realitySni"]+" / "+config["realityShortId"])
			}
		}

		if line.Type == LineTypeCloudflare && truthy(config["nginxApply"]) {
			path := strings.TrimSpace(config["nginxConfigPath"])
			if path == "" {
				path = defaultNginxConfigPath(line.Id)
			}
			if runtime.GOOS == "linux" {
				if _, statErr := os.Stat(path); statErr != nil {
					add("Nginx 配置", "fail", statErr.Error())
				} else {
					add("Nginx 配置", "pass", path)
				}
			} else {
				add("Nginx 配置", "pass", "本机开发环境跳过文件检查")
			}
		} else if line.Type == LineTypeCloudflare {
			add("Nginx 配置", "pass", "未启用真实写入，只使用配置草案")
		}
	}

	passCount, warnCount, failCount := countLineCheckItems(items)
	status := "active"
	if failCount > 0 {
		status = "failed"
	} else if warnCount > 0 {
		status = "warning"
	}
	resp := &LineCheckResponse{
		Status:    status,
		PassCount: passCount,
		WarnCount: warnCount,
		FailCount: failCount,
		Items:     items,
	}

	itemsJSON := mustJSON(items)
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.LineCheckResult{
			LineId:    line.Id,
			Status:    status,
			PassCount: passCount,
			WarnCount: warnCount,
			FailCount: failCount,
			ItemsJSON: itemsJSON,
		}).Error; err != nil {
			return err
		}
		if err := createLineLog(tx, line.Id, "check", lineStatusToLogLevel(status), "线路检测完成", fmt.Sprintf("通过 %d，警告 %d，失败 %d", passCount, warnCount, failCount)); err != nil {
			return err
		}
		return tx.Model(&model.LineProfile{}).Where("id = ?", line.Id).Updates(map[string]any{
			"status":        status,
			"last_check_at": time.Now().Unix(),
			"last_error":    firstFailedLineCheckMessage(items),
		}).Error
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (s *LineService) GetLineShare(id int) (*LineShareResponse, error) {
	line, _, config, inbound, err := loadLineRuntime(id)
	if err != nil {
		return nil, err
	}
	if inbound == nil {
		return nil, fmt.Errorf("line has not been applied yet")
	}

	clientID, err := firstVlessClientID(inbound.Settings)
	if err != nil {
		return nil, err
	}
	var label string
	var link string
	switch line.Type {
	case LineTypeCloudflare:
		wsPath := strings.TrimSpace(config["wsPath"])
		if wsPath == "" {
			wsPath = "/"
		}
		label = "VLESS WS TLS"
		link = buildCloudflareVlessShareLink(line, clientID, wsPath)
	case LineTypeReality:
		label = "VLESS Reality"
		link, err = buildRealityVlessShareLink(line, clientID, config)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("share link for this line type is not ready yet")
	}
	return &LineShareResponse{
		Links: []LineShareLink{{
			Label: label,
			URI:   link,
		}},
	}, nil
}

type lineApplyFailure struct {
	action  string
	message string
	detail  string
	cause   error
}

func (e *lineApplyFailure) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	if e.detail != "" {
		return e.detail
	}
	return e.message
}

func (e *lineApplyFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newLineApplyFailure(action string, message string, detail string) error {
	return &lineApplyFailure{
		action:  action,
		message: message,
		detail:  detail,
		cause:   errors.New(detail),
	}
}

func wrapLineApplyFailure(action string, message string, err error) error {
	if err == nil {
		return nil
	}
	return &lineApplyFailure{
		action:  action,
		message: message,
		detail:  err.Error(),
		cause:   err,
	}
}

func recordLineApplyFailure(lineID int, err error) error {
	failure := &lineApplyFailure{
		action:  "apply",
		message: "Line apply failed",
		detail:  err.Error(),
		cause:   err,
	}
	var typed *lineApplyFailure
	if errors.As(err, &typed) && typed != nil {
		failure = typed
	}
	if failure.action == "" {
		failure.action = "apply"
	}
	if failure.message == "" {
		failure.message = "Line apply failed"
	}
	if failure.detail == "" {
		failure.detail = err.Error()
	}

	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		var line model.LineProfile
		if err := tx.First(&line, lineID).Error; err != nil {
			return err
		}
		if err := tx.Model(&line).Updates(map[string]any{
			"status":     "apply_failed",
			"last_error": failure.detail,
		}).Error; err != nil {
			return err
		}
		return createLineLog(tx, lineID, failure.action, "error", failure.message, failure.detail)
	})
}

func createLineLog(tx *gorm.DB, lineID int, action string, level string, message string, detail string) error {
	return tx.Create(&model.LineApplyLog{
		LineId:  lineID,
		Action:  action,
		Level:   level,
		Message: message,
		Detail:  detail,
	}).Error
}

func loadLineRuntime(id int) (model.LineProfile, *model.LineOutbound, map[string]string, *model.Inbound, error) {
	var line model.LineProfile
	if err := database.GetDB().First(&line, id).Error; err != nil {
		return line, nil, nil, nil, err
	}
	config := ensureLineConfigDefaults(line.Id, line.Type, decodeLineConfig(line.ConfigJSON))

	var outbound model.LineOutbound
	var outboundPtr *model.LineOutbound
	err := database.GetDB().Where("line_id = ?", line.Id).Order("id asc").First(&outbound).Error
	if err == nil {
		outboundPtr = &outbound
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return line, nil, nil, nil, err
	}

	var inbound model.Inbound
	var inboundPtr *model.Inbound
	if line.InboundId != nil && *line.InboundId > 0 {
		err = database.GetDB().First(&inbound, *line.InboundId).Error
		if err == nil {
			inboundPtr = &inbound
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return line, outboundPtr, config, nil, err
		}
	}
	if inboundPtr == nil {
		err = database.GetDB().Where("tag = ?", fmt.Sprintf("line-%d-in", line.Id)).First(&inbound).Error
		if err == nil {
			inboundPtr = &inbound
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return line, outboundPtr, config, nil, err
		}
	}

	return line, outboundPtr, config, inboundPtr, nil
}

func lineTemplateHasRoute(outboundTag string, inboundTag string) (bool, bool, error) {
	var setting model.Setting
	err := database.GetDB().Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(setting.Value), &cfg); err != nil {
		return false, false, fmt.Errorf("xrayTemplateConfig invalid: %w", err)
	}
	hasOutbound := false
	for _, item := range asObjectSlice(cfg["outbounds"]) {
		if tag, _ := item["tag"].(string); tag == outboundTag {
			hasOutbound = true
			break
		}
	}
	routing := asObjectMap(cfg["routing"])
	hasRoute := false
	for _, item := range asObjectSlice(routing["rules"]) {
		if out, _ := item["outboundTag"].(string); out != outboundTag {
			continue
		}
		tags, ok := stringSliceFromAny(item["inboundTag"])
		if ok && len(tags) == 1 && tags[0] == inboundTag {
			hasRoute = true
			break
		}
	}
	return hasOutbound, hasRoute, nil
}

func firstVlessClientID(settingsJSON string) (string, error) {
	var settings map[string]any
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return "", fmt.Errorf("inbound settings invalid: %w", err)
	}
	rawClients, ok := settings["clients"].([]any)
	if !ok || len(rawClients) == 0 {
		return "", fmt.Errorf("line has no VLESS client")
	}
	client, ok := rawClients[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("line client settings invalid")
	}
	clientID, _ := client["id"].(string)
	if strings.TrimSpace(clientID) == "" {
		return "", fmt.Errorf("line client id is empty")
	}
	return clientID, nil
}

func buildCloudflareVlessShareLink(line model.LineProfile, clientID string, wsPath string) string {
	host := strings.TrimSpace(line.EntryHost)
	params := url.Values{}
	params.Set("type", "ws")
	params.Set("encryption", "none")
	params.Set("path", wsPath)
	params.Set("host", host)
	params.Set("security", "tls")
	params.Set("sni", host)
	params.Set("fp", "chrome")
	return fmt.Sprintf("vless://%s@%s:%d?%s#%s", clientID, formatShareHost(host), line.EntryPort, params.Encode(), url.QueryEscape(line.Name))
}

func buildRealityVlessShareLink(line model.LineProfile, clientID string, config map[string]string) (string, error) {
	host := strings.TrimSpace(line.EntryHost)
	if host == "" {
		return "", fmt.Errorf("entry host is required")
	}
	publicKey := strings.TrimSpace(config["realityPublicKey"])
	if publicKey == "" {
		return "", fmt.Errorf("reality public key is missing")
	}
	shortID := strings.TrimSpace(config["realityShortId"])
	if shortID == "" {
		return "", fmt.Errorf("reality short id is missing")
	}
	sni := strings.TrimSpace(config["realitySni"])
	if sni == "" {
		return "", fmt.Errorf("reality sni is missing")
	}
	flow := strings.TrimSpace(config["realityFlow"])
	if flow == "" {
		flow = "xtls-rprx-vision"
	}
	fingerprint := strings.TrimSpace(config["realityFingerprint"])
	if fingerprint == "" {
		fingerprint = "chrome"
	}
	spiderX := strings.TrimSpace(config["realitySpiderX"])
	if spiderX == "" {
		spiderX = "/"
	}

	params := url.Values{}
	params.Set("type", "tcp")
	params.Set("encryption", "none")
	params.Set("security", "reality")
	params.Set("pbk", publicKey)
	params.Set("fp", fingerprint)
	params.Set("sni", sni)
	params.Set("sid", shortID)
	params.Set("spx", spiderX)
	params.Set("flow", flow)
	return fmt.Sprintf("vless://%s@%s:%d?%s#%s", clientID, formatShareHost(host), line.EntryPort, params.Encode(), url.QueryEscape(line.Name)), nil
}

func formatShareHost(host string) string {
	host = strings.TrimSpace(host)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

func countLineCheckItems(items []LineCheckItem) (int, int, int) {
	passCount, warnCount, failCount := 0, 0, 0
	for _, item := range items {
		switch item.Status {
		case "pass":
			passCount++
		case "warn":
			warnCount++
		case "fail":
			failCount++
		}
	}
	return passCount, warnCount, failCount
}

func lineStatusToLogLevel(status string) string {
	switch status {
	case "failed":
		return "error"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}

func firstFailedLineCheckMessage(items []LineCheckItem) string {
	for _, item := range items {
		if item.Status == "fail" {
			return item.Name + ": " + item.Message
		}
	}
	return ""
}

func validateLineForApply(line *model.LineProfile, outbound *model.LineOutbound, config map[string]string) error {
	if line == nil {
		return fmt.Errorf("line is required")
	}
	if outbound == nil {
		return fmt.Errorf("residential outbound config is required")
	}
	if line.Type != LineTypeCloudflare && line.Type != LineTypeReality {
		return fmt.Errorf("unsupported line type for MVP apply: %s", line.Type)
	}
	if err := validateHostValue("entry host", line.EntryHost); err != nil {
		return err
	}
	if err := validatePortValue("entry port", line.EntryPort); err != nil {
		return err
	}
	outbound.Type = strings.ToLower(strings.TrimSpace(outbound.Type))
	if outbound.Type != "socks5" && outbound.Type != "http" && outbound.Type != "https" {
		return fmt.Errorf("residential outbound type is required")
	}
	if err := validateHostValue("residential outbound host", outbound.Host); err != nil {
		return err
	}
	if err := validatePortValue("residential outbound port", outbound.Port); err != nil {
		return err
	}

	switch line.Type {
	case LineTypeCloudflare:
		wsPath, err := validateWSPath(config["wsPath"])
		if err != nil {
			return err
		}
		config["wsPath"] = wsPath
		localPort, err := parsePortString(config["localXrayPort"])
		if err != nil {
			return fmt.Errorf("local Xray port invalid: %w", err)
		}
		if localPort == line.EntryPort {
			return fmt.Errorf("local Xray port must not equal public entry port: %d", localPort)
		}
	case LineTypeReality:
		if err := ensureRealityConfig(config); err != nil {
			return err
		}
		if err := validateRealityConfig(config); err != nil {
			return err
		}
	}
	return nil
}

func validatePortValue(label string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("%s is required and must be between 1 and 65535", label)
	}
	return nil
}

func validateHostValue(label string, host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("%s is required", label)
	}
	if strings.ContainsAny(host, " \t\r\n/\\") {
		return fmt.Errorf("%s is invalid: %s", label, host)
	}
	return nil
}

func validateWSPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("Cloudflare WS path is required")
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("Cloudflare WS path must start with /")
	}
	if strings.ContainsAny(path, " \t\r\n?#") {
		return "", fmt.Errorf("Cloudflare WS path must not contain whitespace, query, or fragment")
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil {
		return "", fmt.Errorf("Cloudflare WS path is invalid: %w", err)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" {
		return "", fmt.Errorf("Cloudflare WS path is invalid")
	}
	return path, nil
}

func validateRealityConfig(config map[string]string) error {
	if err := validateHostValue("Reality SNI", config["realitySni"]); err != nil {
		return err
	}
	if err := validateRealityDest(config["realityDest"]); err != nil {
		return err
	}
	if err := validateRealityShortID(config["realityShortId"]); err != nil {
		return err
	}
	if err := validateUUIDValue("Reality client ID", config["clientId"]); err != nil {
		return err
	}
	if err := validateX25519Key("Reality private key", config["realityPrivateKey"]); err != nil {
		return err
	}
	return validateX25519Key("Reality public key", config["realityPublicKey"])
}

func validateRealityDest(raw string) error {
	dest := strings.TrimSpace(raw)
	if dest == "" {
		return fmt.Errorf("Reality dest is required")
	}
	host, portRaw, err := net.SplitHostPort(dest)
	if err != nil {
		return fmt.Errorf("Reality dest must be host:port: %w", err)
	}
	if err := validateHostValue("Reality dest host", host); err != nil {
		return err
	}
	_, err = parsePortString(portRaw)
	if err != nil {
		return fmt.Errorf("Reality dest port invalid: %w", err)
	}
	return nil
}

func validateRealityShortID(shortID string) error {
	shortID = strings.TrimSpace(shortID)
	if shortID == "" {
		return fmt.Errorf("Reality Short ID is required")
	}
	if len(shortID) > 16 || len(shortID)%2 != 0 {
		return fmt.Errorf("Reality Short ID must be even-length hex up to 16 characters")
	}
	if _, err := hex.DecodeString(shortID); err != nil {
		return fmt.Errorf("Reality Short ID must be hex: %w", err)
	}
	return nil
}

func validateUUIDValue(label string, value string) error {
	if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
		return fmt.Errorf("%s is invalid: %w", label, err)
	}
	return nil
}

func validateX25519Key(label string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("%s must be base64url without padding: %w", label, err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("%s must decode to 32 bytes", label)
	}
	return nil
}

func applyLineXray(tx *gorm.DB, line *model.LineProfile, outbound *model.LineOutbound, config map[string]string) (int, error) {
	switch line.Type {
	case LineTypeCloudflare:
		return applyCloudflareXray(tx, line, outbound, config)
	case LineTypeReality:
		return applyRealityXray(tx, line, outbound, config)
	default:
		return 0, fmt.Errorf("line type %s has no xray executor", line.Type)
	}
}

func applyCloudflareXray(tx *gorm.DB, line *model.LineProfile, outbound *model.LineOutbound, config map[string]string) (int, error) {
	if line == nil || outbound == nil {
		return 0, fmt.Errorf("line and outbound are required")
	}
	localPort, err := parsePortString(config["localXrayPort"])
	if err != nil {
		return 0, fmt.Errorf("local Xray port invalid: %w", err)
	}
	wsPath := strings.TrimSpace(config["wsPath"])
	if wsPath == "" {
		return 0, fmt.Errorf("ws path is required")
	}
	if !strings.HasPrefix(wsPath, "/") {
		wsPath = "/" + wsPath
		config["wsPath"] = wsPath
	}
	if strings.TrimSpace(outbound.Host) == "" || outbound.Port <= 0 {
		return 0, fmt.Errorf("residential outbound host and port are required")
	}

	inboundTag := fmt.Sprintf("line-%d-in", line.Id)
	outboundTag := line.OutboundTag
	if outboundTag == "" {
		outboundTag = fmt.Sprintf("line-%d-out", line.Id)
		line.OutboundTag = outboundTag
	}

	var existing model.Inbound
	var existingID int
	if line.InboundId != nil && *line.InboundId > 0 {
		if err := tx.First(&existing, *line.InboundId).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		existingID = existing.Id
	}
	if existingID == 0 {
		err := tx.Where("tag = ?", inboundTag).First(&existing).Error
		if err == nil {
			existingID = existing.Id
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
	}

	var conflictCount int64
	if err := tx.Model(&model.Inbound{}).
		Where("node_id IS NULL AND port = ? AND id <> ?", localPort, existingID).
		Count(&conflictCount).Error; err != nil {
		return 0, err
	}
	if conflictCount > 0 {
		return 0, fmt.Errorf("local Xray port %d is already used by another inbound", localPort)
	}

	inbound := buildCloudflareInbound(line, inboundTag, localPort, wsPath, config)
	if existingID > 0 {
		inbound.Id = existingID
		if err := tx.Model(&existing).Updates(map[string]any{
			"user_id":             inbound.UserId,
			"remark":              inbound.Remark,
			"sub_sort_index":      inbound.SubSortIndex,
			"enable":              inbound.Enable,
			"traffic_reset":       inbound.TrafficReset,
			"traffic_reset_day":   inbound.TrafficResetDay,
			"listen":              inbound.Listen,
			"port":                inbound.Port,
			"protocol":            inbound.Protocol,
			"settings":            inbound.Settings,
			"stream_settings":     inbound.StreamSettings,
			"sniffing":            inbound.Sniffing,
			"tag":                 inbound.Tag,
			"share_addr_strategy": inbound.ShareAddrStrategy,
			"share_addr":          inbound.ShareAddr,
		}).Error; err != nil {
			return 0, err
		}
	} else {
		if err := tx.Create(inbound).Error; err != nil {
			return 0, err
		}
	}
	if err := upsertLineManagedClient(tx, line, inbound.Id, config); err != nil {
		return 0, err
	}

	if err := upsertLineOutboundInTemplate(tx, outboundTag, inboundTag, outbound); err != nil {
		return 0, err
	}
	if err := tx.Model(line).Updates(map[string]any{
		"inbound_id":   inbound.Id,
		"outbound_tag": outboundTag,
		"config_json":  encodeLineConfig(config),
	}).Error; err != nil {
		return 0, err
	}
	if err := tx.Model(outbound).Update("tag", outboundTag).Error; err != nil {
		return 0, err
	}

	return inbound.Id, nil
}

func applyRealityXray(tx *gorm.DB, line *model.LineProfile, outbound *model.LineOutbound, config map[string]string) (int, error) {
	if line == nil || outbound == nil {
		return 0, fmt.Errorf("line and outbound are required")
	}
	if strings.TrimSpace(line.EntryHost) == "" {
		return 0, fmt.Errorf("reality entry host is required")
	}
	if line.EntryPort <= 0 || line.EntryPort > 65535 {
		return 0, fmt.Errorf("reality entry port is invalid: %d", line.EntryPort)
	}
	if strings.TrimSpace(outbound.Host) == "" || outbound.Port <= 0 {
		return 0, fmt.Errorf("residential outbound host and port are required")
	}
	if err := ensureRealityConfig(config); err != nil {
		return 0, err
	}

	inboundTag := fmt.Sprintf("line-%d-in", line.Id)
	outboundTag := line.OutboundTag
	if outboundTag == "" {
		outboundTag = fmt.Sprintf("line-%d-out", line.Id)
		line.OutboundTag = outboundTag
	}

	var existing model.Inbound
	var existingID int
	if line.InboundId != nil && *line.InboundId > 0 {
		if err := tx.First(&existing, *line.InboundId).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		existingID = existing.Id
	}
	if existingID == 0 {
		err := tx.Where("tag = ?", inboundTag).First(&existing).Error
		if err == nil {
			existingID = existing.Id
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
	}

	var conflictCount int64
	if err := tx.Model(&model.Inbound{}).
		Where("node_id IS NULL AND port = ? AND id <> ?", line.EntryPort, existingID).
		Count(&conflictCount).Error; err != nil {
		return 0, err
	}
	if conflictCount > 0 {
		return 0, fmt.Errorf("reality port %d is already used by another inbound", line.EntryPort)
	}

	inbound := buildRealityInbound(line, inboundTag, config)
	if existingID > 0 {
		inbound.Id = existingID
		if err := tx.Model(&existing).Updates(map[string]any{
			"user_id":             inbound.UserId,
			"remark":              inbound.Remark,
			"sub_sort_index":      inbound.SubSortIndex,
			"enable":              inbound.Enable,
			"traffic_reset":       inbound.TrafficReset,
			"traffic_reset_day":   inbound.TrafficResetDay,
			"listen":              inbound.Listen,
			"port":                inbound.Port,
			"protocol":            inbound.Protocol,
			"settings":            inbound.Settings,
			"stream_settings":     inbound.StreamSettings,
			"sniffing":            inbound.Sniffing,
			"tag":                 inbound.Tag,
			"share_addr_strategy": inbound.ShareAddrStrategy,
			"share_addr":          inbound.ShareAddr,
		}).Error; err != nil {
			return 0, err
		}
	} else {
		if err := tx.Create(inbound).Error; err != nil {
			return 0, err
		}
	}
	if err := upsertLineManagedClient(tx, line, inbound.Id, config); err != nil {
		return 0, err
	}

	if err := upsertLineOutboundInTemplate(tx, outboundTag, inboundTag, outbound); err != nil {
		return 0, err
	}
	if err := tx.Model(line).Updates(map[string]any{
		"inbound_id":   inbound.Id,
		"outbound_tag": outboundTag,
		"config_json":  encodeLineConfig(config),
	}).Error; err != nil {
		return 0, err
	}
	if err := tx.Model(outbound).Update("tag", outboundTag).Error; err != nil {
		return 0, err
	}

	return inbound.Id, nil
}

func buildCloudflareInbound(line *model.LineProfile, inboundTag string, localPort int, wsPath string, config map[string]string) *model.Inbound {
	clientID := strings.TrimSpace(config["clientId"])
	if clientID == "" {
		clientID = uuid.NewString()
		config["clientId"] = clientID
	}
	subID := fmt.Sprintf("line-%d", line.Id)
	settings := mustJSON(map[string]any{
		"clients": []map[string]any{{
			"id":         clientID,
			"email":      fmt.Sprintf("line-%d-user", line.Id),
			"enable":     true,
			"flow":       "",
			"limitIp":    0,
			"totalGB":    0,
			"expiryTime": 0,
			"tgId":       0,
			"subId":      subID,
			"reset":      0,
		}},
		"decryption": "none",
	})
	stream := mustJSON(map[string]any{
		"network":  "ws",
		"security": "none",
		"wsSettings": map[string]any{
			"path":    wsPath,
			"headers": map[string]any{},
		},
	})
	sniffing := mustJSON(map[string]any{
		"enabled":      true,
		"destOverride": []string{"http", "tls", "quic"},
	})
	return &model.Inbound{
		UserId:            1,
		Remark:            line.Name,
		SubSortIndex:      1,
		Enable:            true,
		TrafficReset:      "never",
		TrafficResetDay:   1,
		Listen:            "127.0.0.1",
		Port:              localPort,
		Protocol:          model.VLESS,
		Settings:          settings,
		StreamSettings:    stream,
		Sniffing:          sniffing,
		Tag:               inboundTag,
		ShareAddrStrategy: "custom",
		ShareAddr:         line.EntryHost,
	}
}

func buildRealityInbound(line *model.LineProfile, inboundTag string, config map[string]string) *model.Inbound {
	clientID := strings.TrimSpace(config["clientId"])
	if clientID == "" {
		clientID = uuid.NewString()
		config["clientId"] = clientID
	}
	flow := strings.TrimSpace(config["realityFlow"])
	if flow == "" {
		flow = "xtls-rprx-vision"
		config["realityFlow"] = flow
	}
	fingerprint := strings.TrimSpace(config["realityFingerprint"])
	if fingerprint == "" {
		fingerprint = "chrome"
		config["realityFingerprint"] = fingerprint
	}
	spiderX := strings.TrimSpace(config["realitySpiderX"])
	if spiderX == "" {
		spiderX = "/"
		config["realitySpiderX"] = spiderX
	}
	dest := strings.TrimSpace(config["realityDest"])
	if dest == "" {
		dest = strings.TrimSpace(config["realitySni"]) + ":443"
		config["realityDest"] = dest
	}

	subID := fmt.Sprintf("line-%d", line.Id)
	settings := mustJSON(map[string]any{
		"clients": []map[string]any{{
			"id":         clientID,
			"email":      fmt.Sprintf("line-%d-user", line.Id),
			"enable":     true,
			"flow":       flow,
			"limitIp":    0,
			"totalGB":    0,
			"expiryTime": 0,
			"tgId":       0,
			"subId":      subID,
			"reset":      0,
		}},
		"decryption": "none",
		"encryption": "none",
		"fallbacks":  []any{},
	})
	stream := mustJSON(map[string]any{
		"network": "tcp",
		"tcpSettings": map[string]any{
			"header": map[string]any{"type": "none"},
		},
		"security": "reality",
		"realitySettings": map[string]any{
			"show":         false,
			"xver":         0,
			"target":       dest,
			"serverNames":  []string{strings.TrimSpace(config["realitySni"])},
			"privateKey":   strings.TrimSpace(config["realityPrivateKey"]),
			"minClientVer": strings.TrimSpace(config["realityMinClientVer"]),
			"maxClientVer": "",
			"maxTimediff":  0,
			"shortIds":     []string{strings.TrimSpace(config["realityShortId"])},
			"mldsa65Seed":  "",
			"settings": map[string]any{
				"publicKey":     strings.TrimSpace(config["realityPublicKey"]),
				"fingerprint":   fingerprint,
				"serverName":    "",
				"spiderX":       spiderX,
				"mldsa65Verify": "",
			},
		},
	})
	sniffing := mustJSON(map[string]any{
		"enabled":      true,
		"destOverride": []string{"http", "tls", "quic"},
	})
	return &model.Inbound{
		UserId:            1,
		Remark:            line.Name,
		SubSortIndex:      1,
		Enable:            true,
		TrafficReset:      "never",
		TrafficResetDay:   1,
		Listen:            "0.0.0.0",
		Port:              line.EntryPort,
		Protocol:          model.VLESS,
		Settings:          settings,
		StreamSettings:    stream,
		Sniffing:          sniffing,
		Tag:               inboundTag,
		ShareAddrStrategy: "custom",
		ShareAddr:         line.EntryHost,
	}
}

// upsertLineManagedClient keeps the generated share-link identity in 3x-ui's
// normalized client tables. Xray builds active users from these tables, not
// from the legacy clients array persisted in an inbound's settings JSON.
func upsertLineManagedClient(tx *gorm.DB, line *model.LineProfile, inboundID int, config map[string]string) error {
	if tx == nil || line == nil || inboundID <= 0 {
		return fmt.Errorf("line client requires transaction, line, and inbound")
	}
	clientID := strings.TrimSpace(config["clientId"])
	if _, err := uuid.Parse(clientID); err != nil {
		return fmt.Errorf("line client ID is invalid: %w", err)
	}

	email := lineManagedClientEmail(line.Id)
	subID := lineManagedClientSubID(line.Id)
	flow := ""
	if line.Type == LineTypeReality {
		flow = strings.TrimSpace(config["realityFlow"])
	}

	var record model.ClientRecord
	err := tx.Where("email = ?", email).First(&record).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		record = model.ClientRecord{
			Email:  email,
			SubID:  subID,
			UUID:   clientID,
			Flow:   flow,
			Enable: true,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if record.SubID != subID {
			return fmt.Errorf("managed line client email is already owned by another client: %s", email)
		}
		if err := tx.Model(&record).Updates(map[string]any{
			"uuid":   clientID,
			"flow":   flow,
			"enable": true,
			"sub_id": subID,
		}).Error; err != nil {
			return err
		}
	}

	link := model.ClientInbound{ClientId: record.Id, InboundId: inboundID, FlowOverride: flow}
	return tx.Where("client_id = ? AND inbound_id = ?", record.Id, inboundID).
		Assign(map[string]any{"flow_override": flow}).
		FirstOrCreate(&link).Error
}

func removeLineManagedClient(tx *gorm.DB, lineID int) error {
	if tx == nil || lineID <= 0 {
		return nil
	}
	email := lineManagedClientEmail(lineID)
	var record model.ClientRecord
	err := tx.Where("email = ?", email).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.SubID != lineManagedClientSubID(lineID) {
		return fmt.Errorf("refusing to remove unmanaged client with line email: %s", email)
	}
	if err := tx.Where("client_id = ?", record.Id).Delete(&model.ClientInbound{}).Error; err != nil {
		return err
	}
	return tx.Delete(&record).Error
}

func lineManagedClientEmail(lineID int) string {
	return fmt.Sprintf("line-%d-user", lineID)
}

func lineManagedClientSubID(lineID int) string {
	return fmt.Sprintf("line-%d", lineID)
}

func upsertLineOutboundInTemplate(tx *gorm.DB, outboundTag string, inboundTag string, outbound *model.LineOutbound) error {
	raw, err := getXrayTemplateConfigTx(tx)
	if err != nil {
		return err
	}
	cfg := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return fmt.Errorf("xrayTemplateConfig invalid: %w", err)
		}
	}
	outbounds := asObjectSlice(cfg["outbounds"])
	outboundConfig := buildResidentialOutboundConfig(outboundTag, outbound)
	outbounds = upsertObjectByTag(outbounds, outboundTag, outboundConfig)
	cfg["outbounds"] = outbounds

	routing := asObjectMap(cfg["routing"])
	rules := asObjectSlice(routing["rules"])
	rule := map[string]any{
		"type":        "field",
		"inboundTag":  []string{inboundTag},
		"outboundTag": outboundTag,
	}
	rules = upsertRoutingRuleByInboundTag(rules, inboundTag, rule)
	routing["rules"] = rules
	cfg["routing"] = routing

	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	var setting model.Setting
	err = tx.Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&model.Setting{Key: "xrayTemplateConfig", Value: string(body)}).Error
	}
	if err != nil {
		return err
	}
	return tx.Model(&setting).Update("value", string(body)).Error
}

func removeLineTemplateArtifacts(tx *gorm.DB, outboundTag string, inboundTag string) error {
	var setting model.Setting
	err := tx.Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(setting.Value), &cfg); err != nil {
		return fmt.Errorf("xrayTemplateConfig invalid: %w", err)
	}
	cfg["outbounds"] = removeObjectByTag(asObjectSlice(cfg["outbounds"]), outboundTag)
	routing := asObjectMap(cfg["routing"])
	routing["rules"] = removeLineRoutingRules(asObjectSlice(routing["rules"]), inboundTag, outboundTag)
	cfg["routing"] = routing
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return tx.Model(&setting).Update("value", string(body)).Error
}

func getXrayTemplateConfigTx(tx *gorm.DB) (string, error) {
	var setting model.Setting
	err := tx.Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		base := xrayTemplateConfig
		if err := tx.Create(&model.Setting{Key: "xrayTemplateConfig", Value: base}).Error; err != nil {
			return "", err
		}
		return base, nil
	}
	return setting.Value, err
}

func buildResidentialOutboundConfig(outboundTag string, outbound *model.LineOutbound) map[string]any {
	protocol := outbound.Type
	if protocol == "socks5" {
		protocol = "socks"
	}
	if protocol == "https" {
		protocol = "http"
	}
	server := map[string]any{
		"address": outbound.Host,
		"port":    outbound.Port,
	}
	if outbound.Username != "" {
		server["users"] = []map[string]any{{
			"user": outbound.Username,
			"pass": outbound.Password,
		}}
	}
	cfg := map[string]any{
		"tag":      outboundTag,
		"protocol": protocol,
		"settings": map[string]any{
			"servers": []map[string]any{server},
		},
	}
	if outbound.Type == "https" {
		cfg["streamSettings"] = map[string]any{"security": "tls"}
	}
	return cfg
}

func upsertObjectByTag(items []map[string]any, tag string, next map[string]any) []map[string]any {
	for i := range items {
		if existing, _ := items[i]["tag"].(string); existing == tag {
			items[i] = next
			return items
		}
	}
	return append(items, next)
}

func removeObjectByTag(items []map[string]any, tag string) []map[string]any {
	if tag == "" {
		return items
	}
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if existing, _ := item["tag"].(string); existing == tag {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func upsertRoutingRuleByInboundTag(items []map[string]any, inboundTag string, next map[string]any) []map[string]any {
	for i := range items {
		tags, ok := stringSliceFromAny(items[i]["inboundTag"])
		if ok && len(tags) == 1 && tags[0] == inboundTag {
			items[i] = next
			return items
		}
	}
	return append([]map[string]any{next}, items...)
}

func removeLineRoutingRules(items []map[string]any, inboundTag string, outboundTag string) []map[string]any {
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		outbound, _ := item["outboundTag"].(string)
		tags, ok := stringSliceFromAny(item["inboundTag"])
		if ok && len(tags) == 1 && tags[0] == inboundTag && outbound == outboundTag {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func asObjectMap(value any) map[string]any {
	if m, ok := value.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

func asObjectSlice(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items
}

func stringSliceFromAny(value any) ([]string, bool) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func parsePortString(raw string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("port out of range: %d", port)
	}
	return port, nil
}

func mustJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func lineOutboundFromRequest(lineID int, tag string, req LineSaveRequest, existingPassword string) *model.LineOutbound {
	password := existingPassword
	if req.OutboundPassword != "" {
		password = req.OutboundPassword
	}
	return &model.LineOutbound{
		LineId:   lineID,
		Type:     req.OutboundType,
		Host:     req.OutboundHost,
		Port:     req.OutboundPort,
		Username: req.OutboundUsername,
		Password: password,
		Tag:      tag,
		Enabled:  true,
	}
}

func normalizeLineSaveRequest(req LineSaveRequest, id int) (LineSaveRequest, error) {
	req.Type = strings.TrimSpace(req.Type)
	if !isSupportedLineType(req.Type) {
		return req, fmt.Errorf("unsupported line type: %s", req.Type)
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = lineTypeName(req.Type)
	}

	req.EntryHost = strings.TrimSpace(req.EntryHost)
	if req.EntryPort <= 0 {
		req.EntryPort = defaultLinePort(req.Type)
	}
	if req.EntryPort > 65535 {
		return req, fmt.Errorf("entry port out of range: %d", req.EntryPort)
	}

	req.OutboundType = strings.ToLower(strings.TrimSpace(req.OutboundType))
	if req.OutboundType == "" {
		req.OutboundType = "socks5"
	}
	if req.OutboundType != "socks5" && req.OutboundType != "http" && req.OutboundType != "https" {
		return req, fmt.Errorf("unsupported outbound type: %s", req.OutboundType)
	}
	req.OutboundHost = strings.TrimSpace(req.OutboundHost)
	if req.OutboundPort < 0 || req.OutboundPort > 65535 {
		return req, fmt.Errorf("outbound port out of range: %d", req.OutboundPort)
	}
	req.OutboundUsername = strings.TrimSpace(req.OutboundUsername)
	if req.Config == nil {
		req.Config = map[string]string{}
	}
	for key, value := range req.Config {
		k := strings.TrimSpace(key)
		v := strings.TrimSpace(value)
		if k == "" {
			delete(req.Config, key)
			continue
		}
		if k != key {
			delete(req.Config, key)
		}
		req.Config[k] = v
	}

	_ = id
	return req, nil
}

func isSupportedLineType(lineType string) bool {
	switch lineType {
	case LineTypeCloudflare, LineTypeReality:
		return true
	default:
		return false
	}
}

func lineTypeName(lineType string) string {
	switch lineType {
	case LineTypeCloudflare:
		return "Cloudflare 主线路"
	case LineTypeReality:
		return "Reality 直连"
	case LineTypeTrojan:
		return "Trojan 直连"
	default:
		return "新线路"
	}
}

func defaultLinePort(lineType string) int {
	switch lineType {
	case LineTypeCloudflare:
		return 8443
	default:
		return 443
	}
}

func buildLineChainText(lineType string, outboundType string) string {
	outbound := strings.ToUpper(outboundType)
	switch lineType {
	case LineTypeCloudflare:
		return fmt.Sprintf("用户 -> Cloudflare -> Nginx -> Xray 本地入站 -> %s 住宅出口", outbound)
	case LineTypeReality:
		return fmt.Sprintf("用户 -> VPS Reality -> %s 住宅出口", outbound)
	case LineTypeTrojan:
		return fmt.Sprintf("用户 -> VPS Trojan TLS -> %s 住宅出口", outbound)
	default:
		return fmt.Sprintf("用户 -> VPS -> %s 住宅出口", outbound)
	}
}

func ensureLineConfigDefaults(lineID int, lineType string, config map[string]string) map[string]string {
	if config == nil {
		config = map[string]string{}
	}
	if strings.TrimSpace(config["clientId"]) == "" {
		config["clientId"] = uuid.NewString()
	}
	if lineType == LineTypeCloudflare {
		if strings.TrimSpace(config["wsPath"]) == "" {
			config["wsPath"] = fmt.Sprintf("/line-%d-ws", lineID)
		}
		if strings.TrimSpace(config["localXrayPort"]) == "" {
			config["localXrayPort"] = fmt.Sprintf("%d", 30000+lineID)
		}
		if strings.TrimSpace(config["nginxConfigPath"]) == "" {
			config["nginxConfigPath"] = defaultNginxConfigPath(lineID)
		}
	}
	if lineType == LineTypeReality {
		if strings.TrimSpace(config["realitySni"]) == "" {
			config["realitySni"] = "www.microsoft.com"
		}
		if strings.TrimSpace(config["realityDest"]) == "" {
			config["realityDest"] = strings.TrimSpace(config["realitySni"]) + ":443"
		}
		if strings.TrimSpace(config["realityShortId"]) == "" {
			config["realityShortId"] = randomHexString(8, fmt.Sprintf("%08x", lineID))
		}
		if strings.TrimSpace(config["realityFlow"]) == "" {
			config["realityFlow"] = "xtls-rprx-vision"
		}
		if strings.TrimSpace(config["realityFingerprint"]) == "" {
			config["realityFingerprint"] = "chrome"
		}
		if strings.TrimSpace(config["realitySpiderX"]) == "" {
			config["realitySpiderX"] = "/"
		}
	}
	return config
}

func mergePreservedLineConfig(existing map[string]string, next map[string]string) map[string]string {
	if next == nil {
		next = map[string]string{}
	}
	preserveKeys := []string{
		"clientId",
		"realityDest",
		"realityPrivateKey",
		"realityPublicKey",
		"realityFlow",
		"realityFingerprint",
		"realitySpiderX",
		"realityMinClientVer",
		"nginxPendingCertFile",
		"nginxPendingKeyFile",
		"nginxPendingCertExpiresAt",
		"nginxCertMode",
	}
	for _, key := range preserveKeys {
		if strings.TrimSpace(next[key]) == "" && strings.TrimSpace(existing[key]) != "" {
			next[key] = existing[key]
		}
	}
	return next
}

func promotePendingOriginCertificate(config map[string]string) error {
	pendingCertificate := strings.TrimSpace(config["nginxPendingCertFile"])
	pendingKey := strings.TrimSpace(config["nginxPendingKeyFile"])
	if pendingCertificate == "" && pendingKey == "" {
		return nil
	}
	if pendingCertificate == "" || pendingKey == "" {
		return fmt.Errorf("pending origin certificate is incomplete; upload both certificate and private key again")
	}
	if !truthy(config["nginxApply"]) {
		return nil
	}
	config["nginxCertFile"] = pendingCertificate
	config["nginxKeyFile"] = pendingKey
	config["nginxCertMode"] = "managed"
	delete(config, "nginxPendingCertFile")
	delete(config, "nginxPendingKeyFile")
	delete(config, "nginxPendingCertExpiresAt")
	return nil
}

func ensureRealityConfig(config map[string]string) error {
	if strings.TrimSpace(config["clientId"]) == "" {
		config["clientId"] = uuid.NewString()
	}
	sni := strings.TrimSpace(config["realitySni"])
	if sni == "" {
		return fmt.Errorf("reality sni is required")
	}
	if strings.TrimSpace(config["realityDest"]) == "" {
		config["realityDest"] = sni + ":443"
	}
	if strings.TrimSpace(config["realityShortId"]) == "" {
		config["realityShortId"] = randomHexString(8, "")
	}
	if strings.TrimSpace(config["realityFlow"]) == "" {
		config["realityFlow"] = "xtls-rprx-vision"
	}
	if strings.TrimSpace(config["realityFingerprint"]) == "" {
		config["realityFingerprint"] = "chrome"
	}
	if strings.TrimSpace(config["realitySpiderX"]) == "" {
		config["realitySpiderX"] = "/"
	}
	if strings.TrimSpace(config["realityPrivateKey"]) == "" || strings.TrimSpace(config["realityPublicKey"]) == "" {
		privateKey, publicKey, err := generateX25519KeyPair()
		if err != nil {
			return err
		}
		config["realityPrivateKey"] = privateKey
		config["realityPublicKey"] = publicKey
	}
	return nil
}

func generateX25519KeyPair() (string, string, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate reality x25519 keypair: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(privateKey.Bytes()), base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()), nil
}

func randomHexString(bytesLen int, fallback string) string {
	if bytesLen <= 0 {
		return fallback
	}
	body := make([]byte, bytesLen)
	if _, err := rand.Read(body); err != nil {
		return fallback
	}
	return hex.EncodeToString(body)
}

func buildLineApplyPlan(line model.LineProfile, outbound *model.LineOutbound, config map[string]string) *LineApplyPlan {
	outboundType := "socks5"
	if outbound != nil && outbound.Type != "" {
		outboundType = outbound.Type
	}

	plan := &LineApplyPlan{
		Title:        lineTypeName(line.Type),
		Summary:      buildLineSummary(line, outbound, config),
		XrayInbound:  buildXrayInboundPlan(line, config),
		XrayOutbound: buildXrayOutboundPlan(line, outbound),
		Checks: []string{
			"检查入口端口是否监听",
			"检查住宅出口代理是否可连接",
			"检查 Xray 配置语法",
			"检查整条链路是否能访问外网",
		},
	}
	if line.Type == LineTypeCloudflare {
		plan.Nginx = buildNginxPlan(line, config)
		plan.Checks = append([]string{"检查 Nginx 配置语法", "检查 Cloudflare 回源域名"}, plan.Checks...)
	}
	plan.Summary = append(plan.Summary, "住宅出口类型: "+strings.ToUpper(outboundType))
	return plan
}

func buildLineSummary(line model.LineProfile, outbound *model.LineOutbound, config map[string]string) []string {
	summary := []string{
		"入口: " + formatHostPort(line.EntryHost, line.EntryPort),
		"链路: " + line.ChainText,
	}
	if outbound != nil {
		summary = append(summary, "住宅出口: "+formatHostPort(outbound.Host, outbound.Port))
	}
	if line.Type == LineTypeCloudflare {
		summary = append(summary, "WS 路径: "+config["wsPath"])
		summary = append(summary, "Xray 本地端口: "+config["localXrayPort"])
	}
	if line.Type == LineTypeReality {
		summary = append(summary, "Reality SNI: "+config["realitySni"])
		summary = append(summary, "Reality Dest: "+config["realityDest"])
		summary = append(summary, "Reality Short ID: "+config["realityShortId"])
	}
	return summary
}

func buildXrayInboundPlan(line model.LineProfile, config map[string]string) map[string]any {
	tag := fmt.Sprintf("line-%d-in", line.Id)
	switch line.Type {
	case LineTypeCloudflare:
		return map[string]any{
			"tag":      tag,
			"listen":   "127.0.0.1",
			"port":     config["localXrayPort"],
			"protocol": "vless",
			"transport": map[string]any{
				"network": "ws",
				"path":    config["wsPath"],
			},
		}
	case LineTypeReality:
		return map[string]any{
			"tag":      tag,
			"listen":   "0.0.0.0",
			"port":     line.EntryPort,
			"protocol": "vless",
			"security": "reality",
			"reality": map[string]any{
				"sni":         config["realitySni"],
				"dest":        config["realityDest"],
				"shortId":     config["realityShortId"],
				"publicKey":   config["realityPublicKey"],
				"fingerprint": config["realityFingerprint"],
				"flow":        config["realityFlow"],
			},
		}
	case LineTypeTrojan:
		return map[string]any{
			"tag":      tag,
			"listen":   "0.0.0.0",
			"port":     line.EntryPort,
			"protocol": "trojan",
			"security": "tls",
		}
	default:
		return map[string]any{"tag": tag}
	}
}

func buildXrayOutboundPlan(line model.LineProfile, outbound *model.LineOutbound) map[string]any {
	tag := line.OutboundTag
	if tag == "" {
		tag = fmt.Sprintf("line-%d-out", line.Id)
	}
	if outbound == nil {
		return map[string]any{"tag": tag, "protocol": "pending"}
	}
	protocol := outbound.Type
	if protocol == "https" {
		protocol = "http"
	}
	return map[string]any{
		"tag":      tag,
		"protocol": protocol,
		"server":   outbound.Host,
		"port":     outbound.Port,
		"user":     outbound.Username,
	}
}

func buildNginxPlan(line model.LineProfile, config map[string]string) string {
	host := line.EntryHost
	if host == "" {
		host = "_"
	}
	certFile := strings.TrimSpace(config["nginxCertFile"])
	keyFile := strings.TrimSpace(config["nginxKeyFile"])
	tlsConfig := "    # Set nginxCertFile and nginxKeyFile before enabling Nginx apply."
	if certFile != "" && keyFile != "" {
		tlsConfig = fmt.Sprintf("    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols TLSv1.2 TLSv1.3;", certFile, keyFile)
	}
	return fmt.Sprintf(`server {
    listen %d ssl http2;
    server_name %s;

%s

    location %s {
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_pass http://127.0.0.1:%s;
    }
}`, line.EntryPort, host, tlsConfig, config["wsPath"], config["localXrayPort"])
}

func defaultNginxConfigPath(lineID int) string {
	return fmt.Sprintf("/etc/nginx/conf.d/x-ui-line-%d.conf", lineID)
}

func applyCloudflareNginx(line model.LineProfile, config map[string]string) (string, error) {
	return applyCloudflareNginxWithExecutor(line, config, osNginxExecutor{})
}

func applyCloudflareNginxWithExecutor(line model.LineProfile, config map[string]string, executor nginxExecutor) (string, error) {
	path := strings.TrimSpace(config["nginxConfigPath"])
	if path == "" {
		path = defaultNginxConfigPath(line.Id)
	}
	if !truthy(config["nginxApply"]) {
		return fmt.Sprintf("真实写入未启用；Nginx 配置路径预设为 %s", path), nil
	}
	if executor.GOOS() != "linux" {
		return fmt.Sprintf("当前系统是 %s，跳过真实 Nginx 写入；服务器 Linux 环境会执行", executor.GOOS()), nil
	}
	if err := validateNginxConfigPathForGOOS(path, executor.GOOS()); err != nil {
		return "", err
	}
	if err := validateNginxTLSPaths(config); err != nil {
		return "", err
	}

	body := buildNginxPlan(line, config) + "\n"
	return applyNginxConfig(path, body, executor)
}

func validateNginxTLSPaths(config map[string]string) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "Nginx origin certificate path", value: config["nginxCertFile"]},
		{name: "Nginx origin key path", value: config["nginxKeyFile"]},
	} {
		path := strings.TrimSpace(field.value)
		if path == "" {
			return fmt.Errorf("%s is required when writing Nginx configuration", field.name)
		}
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("%s must be an absolute path", field.name)
		}
		if strings.ContainsAny(path, "\r\n;{}\"'") {
			return fmt.Errorf("%s contains an invalid character", field.name)
		}
	}
	return nil
}

func applyNginxConfig(path string, body string, executor nginxExecutor) (string, error) {
	backup, backupDetail, err := backupNginxConfig(path, executor)
	if err != nil {
		return "", err
	}
	if err := executor.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create nginx config directory: %w", err)
	}
	if err := executor.WriteFile(path, []byte(body), 0644); err != nil {
		return "", fmt.Errorf("write nginx config: %w", err)
	}
	if output, err := executor.RunCommand("nginx", "-t"); err != nil {
		if restoreErr := restoreNginxConfig(backup, executor); restoreErr != nil {
			return "", fmt.Errorf("nginx -t failed: %w\n%s\nrollback failed: %v", err, output, restoreErr)
		}
		return "", fmt.Errorf("nginx -t failed: %w\n%s\nrolled back nginx config", err, output)
	}
	if output, err := executor.RunCommand("systemctl", "reload", "nginx"); err != nil {
		if fallbackOutput, fallbackErr := executor.RunCommand("service", "nginx", "reload"); fallbackErr != nil {
			if restoreErr := restoreNginxConfig(backup, executor); restoreErr != nil {
				return "", fmt.Errorf("reload nginx failed: systemctl: %w\n%s\nservice: %w\n%s\nrollback failed: %v", err, output, fallbackErr, fallbackOutput, restoreErr)
			}
			return "", fmt.Errorf("reload nginx failed: systemctl: %w\n%s\nservice: %w\n%s\nrolled back nginx config", err, output, fallbackErr, fallbackOutput)
		}
	}
	return fmt.Sprintf("已写入 %s，并通过 nginx -t 后 reload；%s", path, backupDetail), nil
}

type nginxExecutor interface {
	GOOS() string
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, body []byte, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
	Remove(path string) error
	RunCommand(name string, args ...string) (string, error)
	NowUnix() int64
}

func removeLineNginxConfig(line model.LineProfile, config map[string]string) (*nginxConfigBackup, error) {
	if line.Type != LineTypeCloudflare || !truthy(config["nginxApply"]) || runtime.GOOS != "linux" {
		return nil, nil
	}
	path := strings.TrimSpace(config["nginxConfigPath"])
	if path == "" {
		path = defaultNginxConfigPath(line.Id)
	}
	return removeNginxConfig(path, osNginxExecutor{})
}

func removeNginxConfig(path string, executor nginxExecutor) (*nginxConfigBackup, error) {
	if err := validateNginxConfigPathForGOOS(path, executor.GOOS()); err != nil {
		return nil, err
	}
	backup, _, err := backupNginxConfig(path, executor)
	if err != nil {
		return nil, err
	}
	if !backup.existed {
		return backup, nil
	}
	if err := executor.Remove(path); err != nil {
		return nil, fmt.Errorf("remove nginx config: %w", err)
	}
	if output, err := executor.RunCommand("nginx", "-t"); err != nil {
		_ = restoreNginxConfig(backup, executor)
		return nil, fmt.Errorf("nginx -t after remove: %w: %s", err, strings.TrimSpace(output))
	}
	if output, err := executor.RunCommand("nginx", "-s", "reload"); err != nil {
		_ = restoreNginxConfig(backup, executor)
		_, _ = executor.RunCommand("nginx", "-s", "reload")
		return nil, fmt.Errorf("nginx reload after remove: %w: %s", err, strings.TrimSpace(output))
	}
	return backup, nil
}

func restoreRemovedLineNginxConfig(backup *nginxConfigBackup) error {
	if backup == nil {
		return nil
	}
	executor := osNginxExecutor{}
	if err := restoreNginxConfig(backup, executor); err != nil {
		return err
	}
	if !backup.existed || executor.GOOS() != "linux" {
		return nil
	}
	if _, err := executor.RunCommand("nginx", "-t"); err != nil {
		return err
	}
	_, err := executor.RunCommand("nginx", "-s", "reload")
	return err
}

type osNginxExecutor struct{}

func (osNginxExecutor) GOOS() string {
	return runtime.GOOS
}

func (osNginxExecutor) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (osNginxExecutor) WriteFile(path string, body []byte, perm os.FileMode) error {
	return os.WriteFile(path, body, perm)
}

func (osNginxExecutor) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (osNginxExecutor) Remove(path string) error {
	return os.Remove(path)
}

func (osNginxExecutor) RunCommand(name string, args ...string) (string, error) {
	return runCommandOutput(name, args...)
}

func (osNginxExecutor) NowUnix() int64 {
	return time.Now().Unix()
}

type nginxConfigBackup struct {
	path       string
	existed    bool
	body       []byte
	backupPath string
}

func backupNginxConfig(path string, executor nginxExecutor) (*nginxConfigBackup, string, error) {
	body, err := executor.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &nginxConfigBackup{path: path}, "no previous nginx config", nil
		}
		return nil, "", fmt.Errorf("read existing nginx config: %w", err)
	}
	backupPath := fmt.Sprintf("%s.bak.%d", path, executor.NowUnix())
	if err := executor.WriteFile(backupPath, body, 0644); err != nil {
		return nil, "", fmt.Errorf("backup nginx config: %w", err)
	}
	return &nginxConfigBackup{path: path, existed: true, body: body, backupPath: backupPath}, "backup=" + backupPath, nil
}

func restoreNginxConfig(backup *nginxConfigBackup, executor nginxExecutor) error {
	if backup == nil || backup.path == "" {
		return nil
	}
	if backup.existed {
		return executor.WriteFile(backup.path, backup.body, 0644)
	}
	if err := executor.Remove(backup.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validateNginxConfigPath(path string) error {
	return validateNginxConfigPathForGOOS(path, runtime.GOOS)
}

func validateNginxConfigPathForGOOS(path string, goos string) error {
	if goos == "linux" {
		clean := pathpkg.Clean(path)
		if !strings.HasPrefix(clean, "/") {
			return fmt.Errorf("nginx config path must be absolute")
		}
		if !strings.HasPrefix(clean, "/etc/nginx/") {
			return fmt.Errorf("nginx config path must be under /etc/nginx")
		}
		if !strings.HasSuffix(clean, ".conf") {
			return fmt.Errorf("nginx config path must end with .conf")
		}
		return nil
	}

	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return fmt.Errorf("nginx config path must be absolute")
	}
	clean = filepath.ToSlash(clean)
	if !strings.HasPrefix(clean, "/etc/nginx/") {
		return fmt.Errorf("nginx config path must be under /etc/nginx")
	}
	if !strings.HasSuffix(clean, ".conf") {
		return fmt.Errorf("nginx config path must end with .conf")
	}
	return nil
}

func runCommandOutput(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(output), err
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func formatHostPort(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" && port <= 0 {
		return "未填写"
	}
	if host == "" {
		return fmt.Sprintf(":%d", port)
	}
	if port <= 0 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func encodeLineConfig(config map[string]string) string {
	if len(config) == 0 {
		return "{}"
	}
	body, err := json.Marshal(config)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func decodeLineConfig(raw string) map[string]string {
	config := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return config
	}
	_ = json.Unmarshal([]byte(raw), &config)
	return config
}
