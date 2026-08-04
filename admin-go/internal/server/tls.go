package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/neuvector/manager/admin-go/internal/config"
)

const (
	tlsCertificateConfigured = "configured"
	tlsCertificateEphemeral  = "ephemeral"
)

func managerTLSConfig(cfg config.ServerConfig) (*tls.Config, string, error) {
	configured, err := configuredCertificateAvailable(cfg.CertificateFile, cfg.PrivateKeyFile)
	if err != nil {
		return nil, "", err
	}

	var certificate tls.Certificate
	source := tlsCertificateEphemeral
	if configured {
		certificate, err = tls.LoadX509KeyPair(cfg.CertificateFile, cfg.PrivateKeyFile)
		source = tlsCertificateConfigured
	} else {
		certificate, err = generateManagerCertificate(time.Now())
	}
	if err != nil {
		return nil, "", fmt.Errorf("load manager TLS certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		},
	}, source, nil
}

func configuredCertificateAvailable(certificateFile, privateKeyFile string) (bool, error) {
	certificate, certificateErr := os.Stat(certificateFile)
	key, keyErr := os.Stat(privateKeyFile)
	for path, err := range map[string]error{certificateFile: certificateErr, privateKeyFile: keyErr} {
		if err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect TLS file %q: %w", path, err)
		}
	}
	return certificateErr == nil && keyErr == nil && certificate.Mode().IsRegular() && key.Mode().IsRegular(), nil
}

func generateManagerCertificate(now time.Time) (tls.Certificate, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate RSA key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}

	issuer := pkix.Name{
		CommonName:   "neuvector",
		Organization: []string{"NeuVector"},
		Locality:     []string{"San Jose"},
		Province:     []string{"California"},
		Country:      []string{"USA"},
	}
	subject := pkix.Name{CommonName: "neuvector"}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             now,
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"neuvector"},
	}
	parent := &x509.Certificate{Subject: issuer}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create self-signed certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}, nil
}
