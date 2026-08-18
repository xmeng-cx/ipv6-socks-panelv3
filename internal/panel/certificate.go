package panel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func ensureHY2Certificate(dataDir string, network NetworkInfo, advertiseHost string) (string, string, error) {
	tlsDir := filepath.Join(dataDir, "tls")
	certPath := filepath.Join(tlsDir, "hy2.crt")
	keyPath := filepath.Join(tlsDir, "hy2.key")
	if _, certErr := os.Stat(certPath); certErr == nil {
		if _, keyErr := os.Stat(keyPath); keyErr == nil {
			return certPath, keyPath, nil
		}
	}
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		return "", "", err
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", "", err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "IPv6 SOCKS Panel Hysteria2"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	for _, value := range []string{network.IPv4, advertiseHost} {
		if ip := net.ParseIP(value); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else if value != "" && value != "localhost" {
			template.DNSNames = append(template.DNSNames, value)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		_ = os.Remove(certPath)
		return "", "", err
	}
	return certPath, keyPath, nil
}
