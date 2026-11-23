package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
