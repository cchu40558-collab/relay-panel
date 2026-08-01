package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateOriginCertificatePair(t *testing.T) {
	now := time.Now().UTC()
	certificate, privateKey := testOriginCertificate(t, now.Add(-time.Hour), now.Add(time.Hour))
	expiresAt, err := validateOriginCertificatePair(certificate, privateKey, now)
	if err != nil {
		t.Fatalf("validateOriginCertificatePair() error = %v", err)
	}
	if !expiresAt.After(now) {
		t.Fatalf("expiresAt = %v, want a future date", expiresAt)
	}
}

func TestValidateOriginCertificatePairRejectsMismatchAndExpiredCertificate(t *testing.T) {
	now := time.Now().UTC()
	certificate, _ := testOriginCertificate(t, now.Add(-time.Hour), now.Add(time.Hour))
	_, differentKey := testOriginCertificate(t, now.Add(-time.Hour), now.Add(time.Hour))
	if _, err := validateOriginCertificatePair(certificate, differentKey, now); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched key error = %v", err)
	}

	expiredCertificate, expiredKey := testOriginCertificate(t, now.Add(-2*time.Hour), now.Add(-time.Hour))
	if _, err := validateOriginCertificatePair(expiredCertificate, expiredKey, now); err == nil || !strings.Contains(err.Error(), "has expired") {
		t.Fatalf("expired certificate error = %v", err)
	}
}

func TestWriteManagedOriginCertificateWritesOnlyToLineDirectory(t *testing.T) {
	certificate, privateKey := testOriginCertificate(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	previousRoot := managedNginxCertRoot
	managedNginxCertRoot = t.TempDir()
	t.Cleanup(func() { managedNginxCertRoot = previousRoot })

	certificateFile, keyFile, err := writeManagedOriginCertificate(17, certificate, privateKey)
	if err != nil {
		t.Fatalf("writeManagedOriginCertificate() error = %v", err)
	}
	if !strings.Contains(certificateFile, filepath.Join("line-17")) || !strings.Contains(keyFile, filepath.Join("line-17")) {
		t.Fatalf("managed paths must be scoped to the line: certificate=%s key=%s", certificateFile, keyFile)
	}
	if got, err := os.ReadFile(certificateFile); err != nil || string(got) != string(certificate) {
		t.Fatalf("certificate content = %q, err = %v", string(got), err)
	}
	if got, err := os.ReadFile(keyFile); err != nil || string(got) != string(privateKey) {
		t.Fatalf("key content = %q, err = %v", string(got), err)
	}
}

func testOriginCertificate(t *testing.T, notBefore, notAfter time.Time) ([]byte, []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "origin.example.com"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
}
