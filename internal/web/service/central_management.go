package service

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
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
	centralManagementNginxConfigPath = "/etc/nginx/conf.d/line-panel-central-management.conf"
	centralManagementCertRoot        = "/etc/line-panel/central-management"
	defaultCentralManagementPort     = 2083
)

var centralManagementDomainPattern = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// CentralManagementRequest is intentionally small. The certificate and private
// key travel only in a multipart request and are never persisted in the DB.
type CentralManagementRequest struct {
	Domain string
	Port   int
}

// CentralManagementStatus is safe for the browser and RP Console. File paths
// and private-key material are deliberately omitted.
type CentralManagementStatus struct {
	Enabled              bool   `json:"enabled"`
	Domain               string `json:"domain"`
	Port                 int    `json:"port"`
	BasePath             string `json:"basePath"`
	CertificateSHA256    string `json:"certificateSha256"`
	CertificateExpiresAt int64  `json:"certificateExpiresAt"`
	PanelBoundToLoopback bool   `json:"panelBoundToLoopback"`
	AppliedAt            int64  `json:"appliedAt"`
	LastError            string `json:"lastError"`
}

// CentralManagementService owns the optional, dedicated RP Console ingress.
// It is intentionally independent from per-line Nginx configuration so a line
// edit, line delete, or certificate refresh cannot affect the management path.
type CentralManagementService struct {
	settingService SettingService
}

func (s *CentralManagementService) GetStatus() (*CentralManagementStatus, error) {
	basePath, err := s.settingService.GetBasePath()
	if err != nil {
		return nil, err
	}
	status := &CentralManagementStatus{Port: defaultCentralManagementPort, BasePath: basePath}
	var endpoint model.CentralManagementEndpoint
	err = database.GetDB().First(&endpoint, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	status.Enabled = endpoint.Enabled
	status.Domain = endpoint.Domain
	status.Port = endpoint.Port
	if status.Port == 0 {
		status.Port = defaultCentralManagementPort
	}
	status.CertificateSHA256 = endpoint.CertificateSHA256
	status.CertificateExpiresAt = endpoint.CertificateExpiresAt
	status.PanelBoundToLoopback = endpoint.PanelBoundToLoopback
	status.AppliedAt = endpoint.AppliedAt
	status.LastError = endpoint.LastError
	return status, nil
}

func (s *CentralManagementService) Apply(req CentralManagementRequest, certificatePEM, privateKeyPEM []byte) (*CentralManagementStatus, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("central management HTTPS apply is supported only on Linux servers")
	}
	domain, err := normalizeCentralManagementDomain(req.Domain)
	if err != nil {
		return nil, err
	}
	if !isCloudflareHTTPSPort(req.Port) {
		return nil, fmt.Errorf("port must be one of Cloudflare's HTTPS proxy ports: 443, 2053, 2083, 2087, 2096, 8443")
	}
	expiresAt, fingerprint, err := validateCentralOriginCertificate(domain, certificatePEM, privateKeyPEM, time.Now())
	if err != nil {
		return nil, err
	}
	basePath, err := s.settingService.GetBasePath()
	if err != nil {
		return nil, err
	}
	if err := validateCentralManagementBasePath(basePath); err != nil {
		return nil, err
	}
	previousNginx, _, err := backupNginxConfig(centralManagementNginxConfigPath, osNginxExecutor{})
	if err != nil {
		return nil, err
	}

	certFile, keyFile, versionDir, err := writeCentralManagementCertificate(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, err
	}
	keepVersion := false
	defer func() {
		if !keepVersion {
			_ = os.RemoveAll(versionDir)
		}
	}()

	body := buildCentralManagementNginxConfig(domain, req.Port, basePath, certFile, keyFile)
	if err := applyCentralManagementNginxConfig(body, osNginxExecutor{}); err != nil {
		s.recordError(err)
		return nil, err
	}
	if err := probeCentralManagementListener(req.Port); err != nil {
		rollbackErr := restoreCentralManagementNginxConfig(previousNginx, osNginxExecutor{})
		s.recordError(err)
		if rollbackErr != nil {
			return nil, fmt.Errorf("central management listener probe failed: %w; rollback failed: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("central management listener probe failed; previous Nginx configuration restored: %w", err)
	}
	if err := allowCentralManagementFirewallPort(req.Port); err != nil {
		rollbackErr := restoreCentralManagementNginxConfig(previousNginx, osNginxExecutor{})
		s.recordError(err)
		if rollbackErr != nil {
			return nil, fmt.Errorf("apply firewall rule failed: %w; rollback failed: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("apply firewall rule failed; previous Nginx configuration restored: %w", err)
	}

	endpoint := model.CentralManagementEndpoint{
		Id:                   1,
		Enabled:              true,
		Domain:               domain,
		Port:                 req.Port,
		CertificateFile:      certFile,
		KeyFile:              keyFile,
		CertificateSHA256:    fingerprint,
		CertificateExpiresAt: expiresAt.Unix(),
		PanelBoundToLoopback: false,
		AppliedAt:            time.Now().Unix(),
		LastError:            "",
	}
	if err := database.GetDB().Save(&endpoint).Error; err != nil {
		rollbackErr := restoreCentralManagementNginxConfig(previousNginx, osNginxExecutor{})
		if rollbackErr != nil {
			return nil, fmt.Errorf("save management entry: %w; rollback failed: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("save management entry: %w; previous Nginx configuration restored", err)
	}
	keepVersion = true
	return s.GetStatus()
}

func (s *CentralManagementService) Disable() (*CentralManagementStatus, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("central management HTTPS apply is supported only on Linux servers")
	}
	if err := removeCentralManagementNginxConfig(osNginxExecutor{}); err != nil {
		s.recordError(err)
		return nil, err
	}
	db := database.GetDB()
	var endpoint model.CentralManagementEndpoint
	err := db.First(&endpoint, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		endpoint = model.CentralManagementEndpoint{Id: 1, Enabled: false, PanelBoundToLoopback: false}
		if err := db.Create(&endpoint).Error; err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if err := db.Model(&endpoint).Updates(map[string]any{"enabled": false, "panel_bound_to_loopback": false, "last_error": ""}).Error; err != nil {
		return nil, err
	}
	return s.GetStatus()
}

func (s *CentralManagementService) SetPanelBoundToLoopback(enabled bool) error {
	result := database.GetDB().Model(&model.CentralManagementEndpoint{}).Where("id = ?", 1).Update("panel_bound_to_loopback", enabled)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("central management entry not found")
	}
	return nil
}

func (s *CentralManagementService) recordError(applyErr error) {
	if applyErr == nil {
		return
	}
	message := applyErr.Error()
	if len(message) > 1024 {
		message = message[:1024]
	}
	_ = database.GetDB().Model(&model.CentralManagementEndpoint{}).Where("id = ?", 1).Update("last_error", message).Error
}

func normalizeCentralManagementDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(raw))
	if len(domain) > 253 || !centralManagementDomainPattern.MatchString(domain) {
		return "", fmt.Errorf("domain must be a valid DNS hostname without a scheme, port, wildcard, or path")
	}
	return domain, nil
}

func isCloudflareHTTPSPort(port int) bool {
	switch port {
	case 443, 2053, 2083, 2087, 2096, 8443:
		return true
	default:
		return false
	}
}

func validateCentralOriginCertificate(domain string, certificatePEM, privateKeyPEM []byte, now time.Time) (time.Time, string, error) {
	expiresAt, err := validateOriginCertificatePair(certificatePEM, privateKeyPEM, now)
	if err != nil {
		return time.Time{}, "", err
	}
	block, _ := pem.Decode(certificatePEM)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parse origin certificate: %w", err)
	}
	if err := certificate.VerifyHostname(domain); err != nil {
		return time.Time{}, "", fmt.Errorf("origin certificate does not cover %s: %w", domain, err)
	}
	digest := sha256.Sum256(certificate.Raw)
	return expiresAt, hex.EncodeToString(digest[:]), nil
}

func validateCentralManagementBasePath(basePath string) error {
	if basePath == "" || !strings.HasPrefix(basePath, "/") || !strings.HasSuffix(basePath, "/") || strings.ContainsAny(basePath, "\r\n{};") {
		return fmt.Errorf("configured management path is invalid")
	}
	return nil
}

func writeCentralManagementCertificate(certificatePEM, privateKeyPEM []byte) (string, string, string, error) {
	versionDir := filepath.Join(centralManagementCertRoot, "versions", uuid.NewString())
	if err := os.MkdirAll(versionDir, 0700); err != nil {
		return "", "", "", fmt.Errorf("create central management certificate directory: %w", err)
	}
	for _, dir := range []string{centralManagementCertRoot, filepath.Dir(versionDir), versionDir} {
		if err := os.Chmod(dir, 0700); err != nil {
			_ = os.RemoveAll(versionDir)
			return "", "", "", fmt.Errorf("secure central management certificate directory: %w", err)
		}
	}
	certificateFile := filepath.Join(versionDir, "origin.crt")
	keyFile := filepath.Join(versionDir, "origin.key")
	if err := writePrivateFileAtomically(certificateFile, certificatePEM, 0644); err != nil {
		_ = os.RemoveAll(versionDir)
		return "", "", "", err
	}
	if err := writePrivateFileAtomically(keyFile, privateKeyPEM, 0600); err != nil {
		_ = os.RemoveAll(versionDir)
		return "", "", "", err
	}
	return certificateFile, keyFile, versionDir, nil
}

func buildCentralManagementNginxConfig(domain string, port int, basePath, certificateFile, keyFile string) string {
	return fmt.Sprintf(`server {
    listen %d ssl http2;
    server_name %s;

    ssl_certificate %s;
    ssl_certificate_key %s;
    ssl_protocols TLSv1.2 TLSv1.3;

    location ^~ %s {
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_pass http://127.0.0.1:2053;
    }

    location / {
        return 404;
    }
}
`, port, domain, certificateFile, keyFile, basePath)
}

func applyCentralManagementNginxConfig(body string, executor nginxExecutor) error {
	backup, _, err := backupNginxConfig(centralManagementNginxConfigPath, executor)
	if err != nil {
		return err
	}
	if err := executor.MkdirAll(filepath.Dir(centralManagementNginxConfigPath), 0755); err != nil {
		return fmt.Errorf("create Nginx config directory: %w", err)
	}
	if err := executor.WriteFile(centralManagementNginxConfigPath, []byte(body), 0644); err != nil {
		return fmt.Errorf("write Nginx config: %w", err)
	}
	if output, err := executor.RunCommand("nginx", "-t"); err != nil {
		return rollbackCentralManagementNginx(backup, executor, fmt.Errorf("nginx -t failed: %w: %s", err, strings.TrimSpace(output)))
	}
	if err := reloadNginx(executor); err != nil {
		return rollbackCentralManagementNginx(backup, executor, fmt.Errorf("reload nginx: %w", err))
	}
	return nil
}

func removeCentralManagementNginxConfig(executor nginxExecutor) error {
	backup, _, err := backupNginxConfig(centralManagementNginxConfigPath, executor)
	if err != nil {
		return err
	}
	if !backup.existed {
		return nil
	}
	if err := executor.Remove(centralManagementNginxConfigPath); err != nil {
		return fmt.Errorf("remove Nginx config: %w", err)
	}
	if output, err := executor.RunCommand("nginx", "-t"); err != nil {
		return rollbackCentralManagementNginx(backup, executor, fmt.Errorf("nginx -t after disable failed: %w: %s", err, strings.TrimSpace(output)))
	}
	if err := reloadNginx(executor); err != nil {
		return rollbackCentralManagementNginx(backup, executor, fmt.Errorf("reload nginx after disable: %w", err))
	}
	return nil
}

func restoreCentralManagementNginxConfig(backup *nginxConfigBackup, executor nginxExecutor) error {
	if err := restoreNginxConfig(backup, executor); err != nil {
		return err
	}
	if output, err := executor.RunCommand("nginx", "-t"); err != nil {
		return fmt.Errorf("restored config fails nginx -t: %w: %s", err, strings.TrimSpace(output))
	}
	return reloadNginx(executor)
}

func rollbackCentralManagementNginx(backup *nginxConfigBackup, executor nginxExecutor, cause error) error {
	if err := restoreNginxConfig(backup, executor); err != nil {
		return fmt.Errorf("%w; restore previous config: %v", cause, err)
	}
	if output, err := executor.RunCommand("nginx", "-t"); err != nil {
		return fmt.Errorf("%w; restored config fails nginx -t: %v: %s", cause, err, strings.TrimSpace(output))
	}
	if err := reloadNginx(executor); err != nil {
		return fmt.Errorf("%w; restored config could not reload: %v", cause, err)
	}
	return fmt.Errorf("%w; previous Nginx configuration restored", cause)
}

func reloadNginx(executor nginxExecutor) error {
	if output, err := executor.RunCommand("systemctl", "reload", "nginx"); err == nil {
		return nil
	} else if fallbackOutput, fallbackErr := executor.RunCommand("service", "nginx", "reload"); fallbackErr != nil {
		return fmt.Errorf("systemctl: %w: %s; service: %w: %s", err, strings.TrimSpace(output), fallbackErr, strings.TrimSpace(fallbackOutput))
	}
	return nil
}

func probeCentralManagementListener(port int) error {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		return err
	}
	return connection.Close()
}

func allowCentralManagementFirewallPort(port int) error {
	output, err := runCommandOutput("ufw", "status")
	if err != nil {
		return fmt.Errorf("check UFW status: %w", err)
	}
	if !strings.Contains(output, "Status: active") {
		return nil
	}
	if output, err := runCommandOutput("ufw", "allow", strconv.Itoa(port)+"/tcp", "comment", "Relay Panel central management"); err != nil {
		return fmt.Errorf("allow UFW port %d: %w: %s", port, err, strings.TrimSpace(output))
	}
	return nil
}
