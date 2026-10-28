package web

import (
	"net/http"

	"vorfahrt/pkg/acme"

	"gorm.io/gorm"
)

type Config struct {
	DB           *gorm.DB
	AcmeProvider *acme.Provider
	SMTPDomain   string
	DKIMExpected string
	DKIMSelector string
}

func RegisterRoutes(mux *http.ServeMux, cfg Config) {
	validDomainsFunc := func() []string {
		if cfg.AcmeProvider != nil {
			return cfg.AcmeProvider.Domains()
		}
		return nil
	}

	// API routes
	mux.HandleFunc("/api/accounts", handleAccounts(cfg.DB, validDomainsFunc))
	mux.HandleFunc("/api/accounts/delete", handleDeleteAccount(cfg.DB))
	mux.HandleFunc("/api/messages", handleMessages(cfg.DB))
	mux.HandleFunc("/api/server-status", handleServerStatus(cfg.SMTPDomain))
	mux.HandleFunc("/api/domains", handleDomains(cfg.AcmeProvider, cfg.SMTPDomain, cfg.DKIMExpected, cfg.DKIMSelector))

	// UI & Static assets
	mux.HandleFunc("/style.css", handleStatic("style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/script.js", handleStatic("script.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("/", handleIndex(cfg.AcmeProvider))
}
