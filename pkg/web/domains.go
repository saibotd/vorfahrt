package web

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"

	"vorfahrt/pkg/acme"
)

type DomainStatus struct {
	Domain       string `json:"domain"`
	DKIMExpected string `json:"dkim_expected"`
	DKIMName     string `json:"dkim_name"`
	DKIMFound    bool   `json:"dkim_found"`
	SPFFound     bool   `json:"spf_found"`
	DMARCFound   bool   `json:"dmarc_found"`
	SMTPDomain   string `json:"smtp_domain"`
}

func handleDomains(acmeProv *acme.Provider, smtpDomain, dkimExpected, dkimSelector string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		var domains []string
		if acmeProv != nil {
			if err := acmeProv.Reload(); err != nil {
				log.Println("Error reloading acme.json:", err)
			}
			domains = acmeProv.Domains()
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
	}
}
