package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func generateTestCertPEM(t *testing.T, dnsNames ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func TestMatchDomain(t *testing.T) {
	tests := []struct {
		pattern string
		target  string
		want    bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "EXAMPLE.COM", true},
		{"example.com", "sub.example.com", false},
		{"*.example.com", "mail.example.com", true},
		{"*.example.com", "MAIL.EXAMPLE.COM", true},
		{"*.example.com", "a.b.example.com", false},
		{"*.example.com", "example.com", false},
		{"*.example.com", ".example.com", false},
		{"", "example.com", false},
		{"example.com", "", false},
	}

	for _, tt := range tests {
		if got := MatchDomain(tt.pattern, tt.target); got != tt.want {
			t.Errorf("MatchDomain(%q, %q) = %v, want %v", tt.pattern, tt.target, got, tt.want)
		}
	}
}

func TestTLSConfigDynamicReloadAndSortedDomains(t *testing.T) {
	dir := t.TempDir()
	acmePath := filepath.Join(dir, "acme.json")

	// Start provider when acme.json does not exist yet
	p := &Provider{path: acmePath}
	tlsCfg := p.TLSConfig()
	if tlsCfg == nil {
		t.Fatal("expected non-nil TLSConfig even before certificates are loaded")
	}
	if _, err := tlsCfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "zeta.example.com"}); err == nil {
		t.Fatal("expected error when no certificates are loaded yet")
	}

	cert1, key1 := generateTestCertPEM(t, "zeta.example.com", "alpha.example.com")
	cert2, key2 := generateTestCertPEM(t, "beta.example.com")

	payload := AcmeJSON{
		"resolver1": {
			Certificates: []AcmeCertificate{
				{
					Domain: struct {
						Main string   `json:"main"`
						Sans []string `json:"sans"`
					}{Main: "zeta.example.com", Sans: []string{"alpha.example.com"}},
					Certificate: base64.StdEncoding.EncodeToString(cert1),
					Key:         base64.StdEncoding.EncodeToString(key1),
				},
			},
		},
		"resolver2": {
			Certificates: []AcmeCertificate{
				{
					Domain: struct {
						Main string   `json:"main"`
						Sans []string `json:"sans"`
					}{Main: "beta.example.com"},
					Certificate: base64.StdEncoding.EncodeToString(cert2),
					Key:         base64.StdEncoding.EncodeToString(key2),
				},
			},
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(acmePath, raw, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := p.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Previously returned tlsCfg should now dynamically resolve the newly loaded certificate
	cert, err := tlsCfg.GetCertificate(&tls.ClientHelloInfo{
		ServerName:        "beta.example.com",
		SupportedVersions: []uint16{tls.VersionTLS13},
	})
	if err != nil {
		t.Fatalf("expected GetCertificate to succeed after Reload, got: %v", err)
	}
	if cert == nil || cert.Leaf == nil || cert.Leaf.Subject.CommonName != "beta.example.com" {
		t.Fatalf("unexpected certificate returned: %+v", cert)
	}

	wantDomains := []string{"alpha.example.com", "beta.example.com", "zeta.example.com"}
	if got := p.Domains(); !reflect.DeepEqual(got, wantDomains) {
		t.Errorf("Domains() = %v, want %v", got, wantDomains)
	}
}
