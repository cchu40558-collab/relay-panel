package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const (
	bunnyACMEWebroot  = "/var/lib/line-panel/acme"
	certbotDeployHook = "/etc/letsencrypt/renewal-hooks/deploy/line-panel-nginx-reload"
)

type bunnyApplyPreflight struct {
	sourceConfigJSON    string
	sourceLineUpdatedAt int64
	config              map[string]string
}

func (s *LineService) prepareBunnyApply(id int) (*bunnyApplyPreflight, error) {
	var line model.LineProfile
	if err := database.GetDB().First(&line, id).Error; err != nil {
		return nil, err
	}
	config := ensureLineConfigDefaults(line.Id, line.Type, decodeLineConfig(line.ConfigJSON))
	if err := validateBunnyConfig(config); err != nil {
		return nil, err
	}
	if err := ensureBunnyOriginResolvesHere(config["originHost"]); err != nil {
		return nil, err
	}
	if err := ensureBunnyCertificate(line, config); err != nil {
		return nil, err
	}
	return &bunnyApplyPreflight{
		sourceConfigJSON:    line.ConfigJSON,
		sourceLineUpdatedAt: line.UpdatedAt,
		config:              config,
	}, nil
}

func ensureBunnyOriginResolvesHere(host string) error {
	resolved, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve Bunny origin host %s: %w", host, err)
	}
	local, err := localIPv4Addresses()
	if err != nil {
		return err
	}
	for _, candidate := range resolved {
		if candidate.To4() == nil {
			continue
		}
		if _, ok := local[candidate.String()]; ok {
			return nil
		}
	}
	return fmt.Errorf("Bunny origin host %s must resolve directly to this server", host)
}

func localIPv4Addresses() (map[string]struct{}, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list local network interfaces: %w", err)
	}
	addresses := make(map[string]struct{})
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		values, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, value := range values {
			ip, _, err := net.ParseCIDR(value.String())
			if err == nil && ip.To4() != nil {
				addresses[ip.String()] = struct{}{}
			}
		}
	}
	return addresses, nil
}

func ensureBunnyCertificate(line model.LineProfile, config map[string]string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("automatic Bunny certificates are available only on the Linux server")
	}
	if _, err := exec.LookPath("certbot"); err != nil {
		return fmt.Errorf("certbot is not installed; upgrade Relay Panel with the Bunny-enabled installer")
	}
	if err := ensureBunnyACMEConfig(line, config); err != nil {
		return err
	}

	originHost := strings.TrimSpace(config["originHost"])
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "certbot", "certonly", "--webroot", "-w", bunnyACMEWebroot,
		"-d", originHost, "--non-interactive", "--agree-tos", "--email", strings.TrimSpace(config["acmeEmail"]), "--keep-until-expiring")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("request Let's Encrypt certificate: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	if ctx.Err() != nil {
		return fmt.Errorf("request Let's Encrypt certificate: %w", ctx.Err())
	}

	certificateFile := filepath.Join("/etc/letsencrypt/live", originHost, "fullchain.pem")
	keyFile := filepath.Join("/etc/letsencrypt/live", originHost, "privkey.pem")
	if err := validateManagedCertificateFiles(certificateFile, keyFile, originHost); err != nil {
		return err
	}
	config["nginxCertFile"] = certificateFile
	config["nginxKeyFile"] = keyFile
	config["nginxCertMode"] = "letsencrypt"
	return ensureCertbotDeployHook()
}

func ensureBunnyACMEConfig(line model.LineProfile, config map[string]string) error {
	if err := os.MkdirAll(bunnyACMEWebroot, 0755); err != nil {
		return fmt.Errorf("create ACME webroot: %w", err)
	}
	configPath := strings.TrimSuffix(defaultNginxConfigPath(line.Id), ".conf") + "-acme.conf"
	body := fmt.Sprintf(`server {
    listen 80;
    server_name %s;

    location ^~ /.well-known/acme-challenge/ {
        root %s;
        default_type text/plain;
    }

    location / { return 404; }
}`+"\n", strings.TrimSpace(config["originHost"]), bunnyACMEWebroot)
	_, err := applyNginxConfig(configPath, body, osNginxExecutor{})
	if err != nil {
		return fmt.Errorf("prepare ACME validation site: %w", err)
	}
	config["acmeNginxConfigPath"] = configPath
	return nil
}

func validateManagedCertificateFiles(certificateFile, keyFile, hostname string) error {
	pair, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return fmt.Errorf("load managed certificate: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return fmt.Errorf("managed certificate is empty")
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse managed certificate: %w", err)
	}
	if err := certificate.VerifyHostname(hostname); err != nil {
		return fmt.Errorf("managed certificate does not cover %s: %w", hostname, err)
	}
	return nil
}

func ensureCertbotDeployHook() error {
	if err := os.MkdirAll(filepath.Dir(certbotDeployHook), 0755); err != nil {
		return fmt.Errorf("create certbot deploy-hook directory: %w", err)
	}
	body := []byte("#!/usr/bin/env bash\nset -Eeuo pipefail\nnginx -t\nsystemctl reload nginx\n")
	if err := os.WriteFile(certbotDeployHook, body, 0755); err != nil {
		return fmt.Errorf("write certbot deploy hook: %w", err)
	}
	return nil
}
