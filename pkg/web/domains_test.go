package web

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"vorfahrt/pkg/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestParseBIMIRecord(t *testing.T) {
	logo, pem, ok := parseBIMIRecord("  v=BIMI1; l=https://example.com/logo.svg; a=https://example.com/cert.pem; ")
	if !ok || logo != "https://example.com/logo.svg" || pem != "https://example.com/cert.pem" {
		t.Fatalf("unexpected parse result: ok=%v logo=%q pem=%q", ok, logo, pem)
	}

	logo, pem, ok = parseBIMIRecord("v=BIMI1; l=https://example.com/logo.svg; a=;")
	if !ok || logo != "https://example.com/logo.svg" || pem != "" {
		t.Fatalf("unexpected parse result for empty a=: ok=%v logo=%q pem=%q", ok, logo, pem)
	}

	if _, _, ok := parseBIMIRecord("v=DMARC1; p=reject;"); ok {
		t.Fatal("expected non-BIMI record to return ok=false")
	}

	if _, _, ok := parseBIMIRecord("v=BIMI10; l=https://example.com/logo.svg;"); ok {
		t.Fatal("expected invalid version v=BIMI10 to return ok=false")
	}

	logo, pem, ok = parseBIMIRecord("v=BIMI1; l=https://example.com/logo.svg; a=self;")
	if !ok || logo != "https://example.com/logo.svg" || pem != "" {
		t.Fatalf("expected a=self to leave pem empty, got ok=%v logo=%q pem=%q", ok, logo, pem)
	}
}

func TestValidateTinyPSSVG(t *testing.T) {
	valid := `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps" viewBox="0 0 100 100">
  <title>Example Brand</title>
  <rect width="100" height="100" fill="#000"/>
</svg>`
	if err := validateTinyPSSVG([]byte(valid)); err != nil {
		t.Fatalf("expected valid SVG, got: %v", err)
	}

	cases := []struct {
		name string
		svg  string
	}{
		{"wrong profile", `<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny"><title>X</title></svg>`},
		{"wrong version", `<svg xmlns="http://www.w3.org/2000/svg" version="1.1" baseProfile="tiny-ps"><title>X</title></svg>`},
		{"missing title", `<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps"></svg>`},
		{"script tag", `<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps"><title>X</title><script>alert(1)</script></svg>`},
		{"event attr", `<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps" onload="alert(1)"><title>X</title></svg>`},
	}
	for _, tc := range cases {
		if err := validateTinyPSSVG([]byte(tc.svg)); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestCheckBIMISVGAndPEM(t *testing.T) {
	validSVG := `<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps"><title>Brand</title></svg>`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/logo.svg":
			w.Write([]byte(validSVG))
		case "/big.svg":
			w.Write([]byte(strings.Repeat("a", 33*1024)))
		case "/cert.pem":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := srv.Client()
	if err := checkBIMISVG(client, srv.URL+"/logo.svg"); err != nil {
		t.Fatalf("expected valid SVG over TLS, got: %v", err)
	}
	if err := checkBIMISVG(client, srv.URL+"/big.svg"); err == nil {
		t.Fatal("expected size error for >32KB SVG")
	}
	if err := checkBIMISVG(client, srv.URL+"/missing.svg"); err == nil {
		t.Fatal("expected 404 error")
	}
	if !checkBIMIPEM(client, srv.URL+"/cert.pem") {
		t.Fatal("expected PEM 200 to pass")
	}
	if checkBIMIPEM(client, srv.URL+"/missing.pem") {
		t.Fatal("expected PEM 404 to fail")
	}
}

func TestBIMIRedirectDowngradeAndSSRFProtection(t *testing.T) {
	plainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps"><title>Brand</title></svg>`))
	}))
	defer plainSrv.Close()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plainSrv.URL+"/logo.svg", http.StatusFound)
	}))
	defer tlsSrv.Close()

	client := tlsSrv.Client()
	client.CheckRedirect = safeBIMICheckRedirect
	if err := checkBIMISVG(client, tlsSrv.URL+"/redirect"); err == nil {
		t.Fatal("expected HTTPS -> HTTP redirect to be rejected for SVG")
	}
	if checkBIMIPEM(client, tlsSrv.URL+"/redirect") {
		t.Fatal("expected HTTPS -> HTTP redirect to be rejected for PEM")
	}

	// Verify restricted IP helper blocks loopback, private, and cloud metadata IPs
	for _, ipStr := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "::1", "fc00::1"} {
		if !isRestrictedIP(net.ParseIP(ipStr)) {
			t.Errorf("expected %s to be restricted", ipStr)
		}
	}
	if isRestrictedIP(net.ParseIP("93.184.216.34")) {
		t.Error("expected public IP 93.184.216.34 not to be restricted")
	}
}

func TestDNSRecordMatchers(t *testing.T) {
	if !matchesSPF("v=spf1 a:mail.example.com ~all") {
		t.Error("expected valid SPF to match")
	}
	if matchesSPF("v=spf10 a:mail.example.com ~all") {
		t.Error("expected v=spf10 not to match")
	}

	if !matchesDMARC("v=DMARC1; p=reject;") {
		t.Error("expected valid DMARC to match")
	}
	if matchesDMARC("v=DMARC10; p=reject;") {
		t.Error("expected v=DMARC10 not to match")
	}

	expected := "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A"
	if !matchesDKIM("v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A", expected) {
		t.Error("expected exact DKIM public key to match")
	}
	if !matchesDKIM("v=DKIM1; k=rsa; p=MIIBIjANBgkq hkiG9w0BAQEFAAOCAQ8A", expected) {
		t.Error("expected DKIM public key with whitespace to match")
	}
	if matchesDKIM("v=DKIM1; k=rsa; p=DIFFERENTKEY", expected) {
		t.Error("expected mismatched DKIM public key not to match")
	}
}

func TestAccountsAndBasicAuthHandlers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "web_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.AutoMigrate(&models.Account{})

	// Non-nil validDomains returning empty must reject account creation
	hEmpty := handleAccounts(db, func() []string { return nil })
	req := httptest.NewRequest(http.MethodPost, "/api/accounts", bytes.NewBufferString(`{"domain":"example.com","username":"u1"}`))
	rec := httptest.NewRecorder()
	hEmpty(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when ACME has 0 domains, got %d", rec.Code)
	}

	// Wildcard domain should allow sub.example.com
	hWild := handleAccounts(db, func() []string { return []string{"*.example.com"} })
	req = httptest.NewRequest(http.MethodPost, "/api/accounts", bytes.NewBufferString(`{"domain":"sub.example.com","username":"u1"}`))
	rec = httptest.NewRecorder()
	hWild(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for wildcard match, got %d: %s", rec.Code, rec.Body.String())
	}

	// Delete with ID 0 must return 400
	hDel := handleDeleteAccount(db)
	req = httptest.NewRequest(http.MethodPost, "/api/accounts/delete", bytes.NewBufferString(`{}`))
	rec = httptest.NewRecorder()
	hDel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when deleting id=0, got %d", rec.Code)
	}

	// BasicAuth check
	protected := BasicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "admin", "s3cr3t")
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "wrong")
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad password, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "s3cr3t")
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid BasicAuth, got %d", rec.Code)
	}
}
