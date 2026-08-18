package panel

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func TestEnsureHY2Certificate(t *testing.T) {
	certPath, keyPath, err := ensureHY2Certificate(t.TempDir(), NetworkInfo{IPv4: "192.168.1.14"}, "panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("192.168.1.14"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatal(err)
	}
}
