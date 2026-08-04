package server

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neuvector/manager/admin-go/internal/config"
)

func TestManagerTLSConfigGeneratesEphemeralCertificate(t *testing.T) {
	directory := t.TempDir()
	cfg := config.ServerConfig{
		CertificateFile: filepath.Join(directory, "missing.crt"),
		PrivateKeyFile:  filepath.Join(directory, "missing.key"),
	}
	tlsConfig, source, err := managerTLSConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if source != tlsCertificateEphemeral || tlsConfig.MinVersion != tls.VersionTLS12 || len(tlsConfig.Certificates) != 1 {
		t.Fatalf("source=%q config=%+v", source, tlsConfig)
	}
	certificate, err := x509.ParseCertificate(tlsConfig.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("public key=%T", certificate.PublicKey)
	}
	if publicKey.N.BitLen() != 2048 {
		t.Fatalf("public key bits=%d", publicKey.N.BitLen())
	}
	if certificate.Subject.CommonName != "neuvector" || len(certificate.Issuer.Organization) != 1 || certificate.Issuer.Organization[0] != "NeuVector" || len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != "neuvector" {
		t.Fatalf("subject=%s issuer=%s SANs=%v", certificate.Subject, certificate.Issuer, certificate.DNSNames)
	}
	if certificate.SignatureAlgorithm != x509.SHA256WithRSA || certificate.NotAfter.Sub(certificate.NotBefore) != 365*24*time.Hour || !certificate.BasicConstraintsValid || certificate.IsCA {
		t.Fatalf("signature=%s validity=%s", certificate.SignatureAlgorithm, certificate.NotAfter.Sub(certificate.NotBefore))
	}
	if err := certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate, certificate.Signature); err != nil {
		t.Fatalf("self signature: %v", err)
	}
}

func TestManagerTLSConfigLoadsConfiguredCertificatePair(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	certificate, err := generateManagerCertificate(now)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "manager.crt")
	privateKeyFile := filepath.Join(directory, "manager.key")
	privateKey, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0600); err != nil {
		t.Fatal(err)
	}

	tlsConfig, source, err := managerTLSConfig(config.ServerConfig{CertificateFile: certificateFile, PrivateKeyFile: privateKeyFile})
	if err != nil {
		t.Fatal(err)
	}
	if source != tlsCertificateConfigured || len(tlsConfig.Certificates) != 1 || string(tlsConfig.Certificates[0].Certificate[0]) != string(certificate.Certificate[0]) {
		t.Fatalf("source=%q certificates=%d", source, len(tlsConfig.Certificates))
	}
}

func TestManagerTLSConfigFallsBackWhenPairIsIncomplete(t *testing.T) {
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "manager.crt")
	if err := os.WriteFile(certificateFile, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	_, source, err := managerTLSConfig(config.ServerConfig{
		CertificateFile: certificateFile,
		PrivateKeyFile:  filepath.Join(directory, "missing.key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if source != tlsCertificateEphemeral {
		t.Fatalf("source=%q", source)
	}
}

func TestRunServesHTTPSWithEphemeralCertificate(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	controllerURL, _ := url.Parse("http://127.0.0.1:1/v1")
	cfg := testConfig(controllerURL)
	cfg.Server.Address = address
	cfg.Server.TLS = true
	cfg.Server.CertificateFile = filepath.Join(t.TempDir(), "missing.crt")
	cfg.Server.PrivateKeyFile = filepath.Join(t.TempDir(), "missing.key")
	cfg.Server.ReadHeaderTimeout = time.Second
	cfg.Server.ShutdownTimeout = time.Second
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	supportTempDir := t.TempDir()
	if err := os.Chmod(supportTempDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg.Support = config.SupportConfig{
		Command: executable, TempDir: supportTempDir, Timeout: time.Minute,
		MaxFileBytes: 1 << 20, MaxConcurrent: 1,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec -- the test verifies a generated self-signed certificate
	deadline := time.Now().Add(5 * time.Second)
	var response *http.Response
	for time.Now().Before(deadline) {
		response, err = client.Get("https://" + address + "/gravatar")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("HTTPS request: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("status=%d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}
