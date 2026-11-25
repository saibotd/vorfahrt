package web

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
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

func isRestrictedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func safeBIMIDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses found for %s", host)
	}
	var dialer net.Dialer
	dialer.Timeout = 5 * time.Second
	var lastErr error
	for _, ipAddr := range ips {
		if isRestrictedIP(ipAddr.IP) {
			return nil, fmt.Errorf("blocked request to restricted IP %s", ipAddr.IP.String())
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("failed to dial host")
}

func safeBIMICheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("stopped after 5 redirects")
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return fmt.Errorf("redirect to non-https URL %q is not allowed", req.URL.String())
	}
	return nil
}

var bimiHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		DialContext:         safeBIMIDialContext,
		TLSHandshakeTimeout: 5 * time.Second,
	},
	CheckRedirect: safeBIMICheckRedirect,
}

func lookupTXTWithTimeout(domain string, timeout time.Duration) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return net.DefaultResolver.LookupTXT(ctx, domain)
}

func matchesSPF(txt string) bool {
	lower := strings.ToLower(strings.TrimSpace(txt))
	return lower == "v=spf1" || strings.HasPrefix(lower, "v=spf1 ")
}

func matchesDMARC(txt string) bool {
	lower := strings.ToLower(strings.TrimSpace(txt))
	return lower == "v=dmarc1" || strings.HasPrefix(lower, "v=dmarc1;") || strings.HasPrefix(lower, "v=dmarc1 ")
}

func extractTagValue(txt, targetKey string) (string, bool) {
	for _, part := range strings.Split(txt, ";") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), targetKey) {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

func matchesDKIM(txt, expected string) bool {
	v, ok := extractTagValue(txt, "v")
	if !ok || !strings.EqualFold(v, "DKIM1") {
		return false
	}
	if expected == "" {
		return true
	}
	expectedPub, hasExpected := extractTagValue(expected, "p")
	if !hasExpected || expectedPub == "" {
		return true
	}
	actualPub, hasActual := extractTagValue(txt, "p")
	if !hasActual {
		return false
	}
	normalize := func(s string) string {
		return strings.Join(strings.Fields(s), "")
	}
	return normalize(actualPub) == normalize(expectedPub)
}

func handleDomains(acmeProv *acme.Provider, smtpDomain, dkimExpected, dkimSelector string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var domains []string
		if acmeProv != nil {
			if err := acmeProv.Reload(); err != nil {
				log.Println("Error reloading acme.json:", err)
			}
			domains = acmeProv.Domains()
		}

		statuses := make([]DomainStatus, len(domains))
		sem := make(chan struct{}, 8)
		var wg sync.WaitGroup

		for i, d := range domains {
			wg.Add(1)
			go func(idx int, domain string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				dnsDomain := strings.TrimPrefix(domain, "*.")
				st := DomainStatus{
					Domain:       domain,
					DKIMExpected: dkimExpected,
					DKIMName:     dkimSelector + "._domainkey",
					SMTPDomain:   smtpDomain,
				}

				const dnsTimeout = 5 * time.Second

				// Check DKIM
				txts, _ := lookupTXTWithTimeout(st.DKIMName+"."+dnsDomain, dnsTimeout)
				for _, txt := range txts {
					if matchesDKIM(txt, dkimExpected) {
						st.DKIMFound = true
						break
					}
				}

				// Check SPF
				txts, _ = lookupTXTWithTimeout(dnsDomain, dnsTimeout)
				for _, txt := range txts {
					if matchesSPF(txt) {
						st.SPFFound = true
						break
					}
				}

				// Check DMARC
				txts, _ = lookupTXTWithTimeout("_dmarc."+dnsDomain, dnsTimeout)
				for _, txt := range txts {
					if matchesDMARC(txt) {
						st.DMARCFound = true
						break
					}
				}

				// Check BIMI
				txts, _ = lookupTXTWithTimeout("default._bimi."+dnsDomain, dnsTimeout)
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

				statuses[idx] = st
			}(i, d)
		}

		wg.Wait()
		json.NewEncoder(w).Encode(statuses)
	}
}

func parseBIMIRecord(txt string) (logoURL, pemURL string, ok bool) {
	txt = strings.TrimSpace(txt)
	var validVersion bool
	for i, part := range strings.Split(txt, ";") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		if i == 0 && key == "v" && val == "BIMI1" {
			validVersion = true
			continue
		}
		switch key {
		case "l":
			logoURL = val
		case "a":
			if !strings.EqualFold(val, "self") {
				pemURL = val
			}
		}
	}
	if !validVersion {
		return "", "", false
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
	if resp.Request != nil && resp.Request.URL != nil && !strings.EqualFold(resp.Request.URL.Scheme, "https") {
		return fmt.Errorf("SVG URL redirected to non-https scheme")
	}
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
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.Request != nil && resp.Request.URL != nil && !strings.EqualFold(resp.Request.URL.Scheme, "https") {
		return false
	}
	return resp.StatusCode == http.StatusOK
}
