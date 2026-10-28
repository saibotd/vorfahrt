package main

import (
	"crypto/x509"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"vorfahrt/pkg/acme"
	"vorfahrt/pkg/models"
	"vorfahrt/pkg/queue"
	local_smtp "vorfahrt/pkg/smtp"
	"vorfahrt/pkg/web"

	"github.com/emersion/go-smtp"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

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
		dkimKeyFile = filepath.Join(dataDir, "dkim.key")
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

	// Queue Retention
	queueRetentionStr := os.Getenv("QUEUE_RETENTION")
	queueRetention, err := queue.ParseRetention(queueRetentionStr)
	if err != nil {
		log.Printf("Invalid QUEUE_RETENTION %q: %v. Using default 24h.", queueRetentionStr, err)
		queueRetention = 24 * time.Hour
	}
	if queueRetention > 0 {
		log.Printf("Queue retention set to %s (items older than this will be deleted)", queueRetention)
	} else {
		log.Println("Queue retention cleanup disabled (QUEUE_RETENTION=0)")
	}

	// Start Queue Worker
	worker := &queue.Worker{
		DB:         db,
		DKIMKey:    dkimKey,
		Selector:   dkimSelector,
		SMTPDomain: smtpDomain,
		Retention:  queueRetention,
	}
	go worker.Start()

	// Pre-warm outbound port 25 connectivity check in background
	go web.CheckOutboundPort25(false)

	// TLS config if acme.json is provided
	var tlsProv *acme.Provider
	acmeFile := os.Getenv("ACME_FILE")
	if acmeFile != "" {
		tlsProv = acme.NewProvider(acmeFile)
	}

	// SMTP Server
	be := &local_smtp.Backend{
		DB: db,
		ValidDomains: func() []string {
			if tlsProv != nil {
				return tlsProv.Domains()
			}
			return nil
		},
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

	// Format expected DKIM DNS value
	var dkimExpected string
	if dkimKey != nil {
		if pubKeyBytes, err := x509.MarshalPKIXPublicKey(&dkimKey.PublicKey); err == nil {
			dkimExpected = "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pubKeyBytes)
		}
	}

	// Web API & UI
	mux := http.NewServeMux()
	web.RegisterRoutes(mux, web.Config{
		DB:           db,
		AcmeProvider: tlsProv,
		SMTPDomain:   smtpDomain,
		DKIMExpected: dkimExpected,
		DKIMSelector: dkimSelector,
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
	log.Fatal(http.ListenAndServe(webAddr, web.BasicAuth(mux, webUser, webPass)))
}
