package main

import (
	"embed"
	"log"
	"net/http"
	"os"
	"time"
	"path/filepath"

	"vorfahrt/pkg/acme"
	"vorfahrt/pkg/models"
	"vorfahrt/pkg/queue"
	local_smtp "vorfahrt/pkg/smtp"
	"vorfahrt/pkg/web"

	"github.com/emersion/go-smtp"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net"
	"strings"
)

//go:embed ui/index.html
var uiFS embed.FS

func basicAuth(next http.Handler, username, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if password == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != username || p != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="Vorfahrt"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	log.Println("Starting Vorfahrt...")

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Database
	dbPath := filepath.Join(dataDir, "data.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	db.AutoMigrate(&models.Account{}, &models.Message{})

	// DKIM Key Setup
	dkimKeyFile := os.Getenv("DKIM_KEY_FILE")
	if dkimKeyFile == "" {
		dkimKeyFile = filepath.Join(dataDir, "dkim.key") // default alongside DB
	}
	dkimKey, err := queue.LoadOrGenerateDKIMKey(dkimKeyFile)
	if err != nil {
		log.Println("DKIM disabled: failed to load or generate key:", err)
	}
	dkimSelector := os.Getenv("DKIM_SELECTOR")
	if dkimSelector == "" {
		dkimSelector = "mail"
	}

	smtpDomain := os.Getenv("SMTP_DOMAIN")
	if smtpDomain == "" {
		smtpDomain = "localhost"
	}

	// Start Queue Worker
	worker := &queue.Worker{
		DB:         db,
		DKIMKey:    dkimKey,
		Selector:   dkimSelector,
		SMTPDomain: smtpDomain,
	}
	go worker.Start()

	// TLS config if acme.json is provided
	var tlsProv *acme.Provider
	acmeFile := os.Getenv("ACME_FILE")
	if acmeFile != "" {
		tlsProv = acme.NewProvider(acmeFile)
	}

	validDomainsFunc := func() []string {
		if tlsProv != nil {
			return tlsProv.Domains()
		}
		return nil
	}

	// SMTP Server
	be := &local_smtp.Backend{
		DB:           db,
		ValidDomains: validDomainsFunc,
	}
	s := smtp.NewServer(be)
	
	smtpAddr := os.Getenv("SMTP_ADDR")
	if smtpAddr == "" {
		smtpAddr = ":25"
	}
	s.Addr = smtpAddr
	s.Domain = smtpDomain
	s.ReadTimeout = 10 * time.Second
	s.WriteTimeout = 10 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true

	if tlsProv != nil && tlsProv.TLSConfig() != nil {
		s.TLSConfig = tlsProv.TLSConfig()
	}

	go func() {
		log.Println("Starting SMTP server on", s.Addr)
		if os.Getenv("SMTP_IMPLICIT_TLS") == "true" {
			if err := s.ListenAndServeTLS(); err != nil {
				log.Fatal(err)
			}
		} else {
			if err := s.ListenAndServe(); err != nil {
				log.Fatal(err)
			}
		}
	}()

	// Web API & UI
	mux := http.NewServeMux()
	web.RegisterHandlers(mux, db, validDomainsFunc)
	
	var dkimExpected string
	if dkimKey != nil {
		pubKeyBytes, err := x509.MarshalPKIXPublicKey(&dkimKey.PublicKey)
		if err == nil {
			pubKeyBase64 := base64.StdEncoding.EncodeToString(pubKeyBytes)
			dkimExpected = "v=DKIM1; k=rsa; p=" + pubKeyBase64
		}
	}

	mux.HandleFunc("/api/server-status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		status := struct {
			SMTPDomain   string `json:"smtp_domain"`
			IP           string `json:"ip"`
			PTR          string `json:"ptr"`
			PTRFound     bool   `json:"ptr_found"`
			PTRMatches   bool   `json:"ptr_matches"`
			FCrDNSStatus string `json:"fcrdns_status"` // "ok", "mismatch", "missing_ptr", "missing_a", "unconfigured"
		}{
			SMTPDomain: smtpDomain,
		}

		if smtpDomain == "" || smtpDomain == "localhost" {
			status.FCrDNSStatus = "unconfigured"
			json.NewEncoder(w).Encode(status)
			return
		}

		ips, err := net.LookupIP(smtpDomain)
		if err != nil || len(ips) == 0 {
			status.FCrDNSStatus = "missing_a"
			json.NewEncoder(w).Encode(status)
			return
		}

		status.IP = ips[0].String()
		ptrs, err := net.LookupAddr(status.IP)
		if err != nil || len(ptrs) == 0 {
			status.FCrDNSStatus = "missing_ptr"
			json.NewEncoder(w).Encode(status)
			return
		}

		status.PTRFound = true
		status.PTR = strings.TrimSuffix(ptrs[0], ".")
		if strings.EqualFold(status.PTR, smtpDomain) {
			status.PTRMatches = true
			status.FCrDNSStatus = "ok"
		} else {
			status.FCrDNSStatus = "mismatch"
		}

		json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("/api/domains", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		var domains []string
		if tlsProv != nil {
			if err := tlsProv.Reload(); err != nil {
				log.Println("Error reloading acme.json:", err)
			}
			domains = tlsProv.Domains()
		}

		type DomainStatus struct {
			Domain       string `json:"domain"`
			DKIMExpected string `json:"dkim_expected"`
			DKIMName     string `json:"dkim_name"`
			DKIMFound    bool   `json:"dkim_found"`
			SPFFound     bool   `json:"spf_found"`
			DMARCFound   bool   `json:"dmarc_found"`
			SMTPDomain   string `json:"smtp_domain"`
		}

		var statuses []DomainStatus
		for _, d := range domains {
			st := DomainStatus{
				Domain:       d,
				DKIMExpected: dkimExpected,
				DKIMName:     dkimSelector + "._domainkey",
				SMTPDomain:   smtpDomain,
			}

			// Check DKIM
			txts, _ := net.LookupTXT(st.DKIMName + "." + d)
			for _, txt := range txts {
				if strings.Contains(txt, "v=DKIM1") {
					st.DKIMFound = true
				}
			}

			// Check SPF
			txts, _ = net.LookupTXT(d)
			for _, txt := range txts {
				if strings.HasPrefix(txt, "v=spf1") {
					st.SPFFound = true
				}
			}

			// Check DMARC
			txts, _ = net.LookupTXT("_dmarc." + d)
			for _, txt := range txts {
				if strings.HasPrefix(txt, "v=DMARC1") {
					st.DMARCFound = true
				}
			}

			statuses = append(statuses, st)
		}

		json.NewEncoder(w).Encode(statuses)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if tlsProv != nil {
			if err := tlsProv.Reload(); err != nil {
				log.Println("Error reloading acme.json:", err)
			}
		}
		content, _ := uiFS.ReadFile("ui/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
	})

	webAddr := os.Getenv("WEB_ADDR")
	if webAddr == "" {
		webAddr = ":8080"
	}
	
	webUser := os.Getenv("WEB_USERNAME")
	if webUser == "" {
		webUser = "admin"
	}
	webPass := os.Getenv("WEB_PASSWORD")

	log.Println("Starting Web UI on", webAddr)
	log.Fatal(http.ListenAndServe(webAddr, basicAuth(mux, webUser, webPass)))
}
