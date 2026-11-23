package web

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"vorfahrt/pkg/acme"
)

type DomainStatus struct {
	Domain       string `json:"domain"`
	DKIMExpected string `json:"dkim_expected"`
	DKIMName     string `json:"dkim_name"`
	DKIMFound    bool   `json:"dkim_found"`
	SPFFound     bool   `json:"spf_found"`
	DMARCFound   bool   `json:"dmarc_found"`
	BIMIFound    bool   `json:"bimi_found"`
	BIMISVGOK    bool   `json:"bimi_svg_ok"`
	BIMISVGError string `json:"bimi_svg_error,omitempty"`
	BIMIPEMSet   bool   `json:"bimi_pem_set"`
	BIMIPEMOK    bool   `json:"bimi_pem_ok"`
	SMTPDomain   string `json:"smtp_domain"`
}

var bimiHTTPClient = &http.Client{Timeout: 5 * time.Second}

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

			// Check BIMI
			txts, _ = net.LookupTXT("default._bimi." + d)
			for _, txt := range txts {
				if logoURL, pemURL, ok := parseBIMIRecord(txt); ok {
					st.BIMIFound = true
					if err := checkBIMISVG(bimiHTTPClient, logoURL); err != nil {
						st.BIMISVGOK = false
						st.BIMISVGError = err.Error()
					} else {
						st.BIMISVGOK = true
						st.BIMISVGError = ""
					}
					if pemURL != "" {
						st.BIMIPEMSet = true
						st.BIMIPEMOK = checkBIMIPEM(bimiHTTPClient, pemURL)
					}
					break
				}
			}

			statuses = append(statuses, st)
		}

		json.NewEncoder(w).Encode(statuses)
	}
}

func parseBIMIRecord(txt string) (logoURL, pemURL string, ok bool) {
	txt = strings.TrimSpace(txt)
	if !strings.HasPrefix(txt, "v=BIMI1") {
		return "", "", false
	}
	for _, part := range strings.Split(txt, ";") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(k) {
		case "l":
			logoURL = strings.TrimSpace(v)
		case "a":
			pemURL = strings.TrimSpace(v)
		}
	}
	return logoURL, pemURL, true
}

func checkBIMISVG(client *http.Client, rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("missing l= SVG URL")
	}
	if !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("SVG URL must use https://")
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	const maxSVGBytes = 32 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSVGBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxSVGBytes {
		return fmt.Errorf("SVG exceeds 32KB limit")
	}
	return validateTinyPSSVG(data)
}

func validateTinyPSSVG(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var rootChecked, inTitle, hasTitle bool
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			for _, attr := range t.Attr {
				if strings.HasPrefix(strings.ToLower(attr.Name.Local), "on") {
					return fmt.Errorf("disallowed event attribute %q", attr.Name.Local)
				}
			}
			if !rootChecked {
				rootChecked = true
				if t.Name.Local != "svg" || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") {
					return fmt.Errorf("root element must be <svg>")
				}
				var version, baseProfile string
				for _, attr := range t.Attr {
					switch attr.Name.Local {
					case "version":
						version = attr.Value
					case "baseProfile":
						baseProfile = attr.Value
					}
				}
				if version != "1.2" {
					return fmt.Errorf("svg version must be 1.2 (got %q)", version)
				}
				if baseProfile != "tiny-ps" {
					return fmt.Errorf("svg baseProfile must be tiny-ps (got %q)", baseProfile)
				}
			}
			switch t.Name.Local {
			case "script", "foreignObject":
				return fmt.Errorf("disallowed <%s> element", t.Name.Local)
			case "title":
				inTitle = true
			}
		case xml.EndElement:
			if t.Name.Local == "title" {
				inTitle = false
			}
		case xml.CharData:
			if inTitle && len(bytes.TrimSpace(t)) > 0 {
				hasTitle = true
			}
		}
	}
	if !rootChecked {
		return fmt.Errorf("empty SVG document")
	}
	if !hasTitle {
		return fmt.Errorf("missing <title> element")
	}
	return nil
}

func checkBIMIPEM(client *http.Client, rawURL string) bool {
	if !strings.HasPrefix(rawURL, "https://") {
		return false
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
