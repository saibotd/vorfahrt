package web

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type OutboundStatus struct {
	Status    string    `json:"status"` // "ok" or "blocked"
	Target    string    `json:"target,omitempty"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

var (
	outboundCheckMu    sync.Mutex
	lastOutboundResult *OutboundStatus
)

// CheckOutboundPort25 checks whether outbound connections on TCP port 25 can be established.
// To avoid alerting or spamming actual mail provider filters (Google, Microsoft) with connection probes,
// it first tests against portquiz.net:25 (a dedicated open service for firewall testing).
// Only if that fails does it fall back to a standard SMTP host with a polite QUIT command.
func CheckOutboundPort25(force bool) OutboundStatus {
	outboundCheckMu.Lock()
	defer outboundCheckMu.Unlock()

	if !force && lastOutboundResult != nil && time.Since(lastOutboundResult.CheckedAt) < 10*time.Minute {
		return *lastOutboundResult
	}

	// 1. Primary target: portquiz.net:25
	conn, err := net.DialTimeout("tcp", "portquiz.net:25", 3*time.Second)
	if err == nil {
		conn.Close()
		res := OutboundStatus{
			Status:    "ok",
			Target:    "portquiz.net:25",
			CheckedAt: time.Now(),
		}
		lastOutboundResult = &res
		return res
	}

	// 2. Fallback: only if portquiz is unreachable, test standard SMTP server with graceful QUIT
	fallbackAddr := "smtp.gmail.com:25"
	conn, fbErr := net.DialTimeout("tcp", fallbackAddr, 3*time.Second)
	if fbErr == nil {
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 256)
		conn.Read(buf)
		conn.Write([]byte("QUIT\r\n"))
		conn.Close()
		res := OutboundStatus{
			Status:    "ok",
			Target:    fallbackAddr,
			CheckedAt: time.Now(),
		}
		lastOutboundResult = &res
		return res
	}

	res := OutboundStatus{
		Status:    "blocked",
		Error:     fbErr.Error(),
		CheckedAt: time.Now(),
	}
	lastOutboundResult = &res
	return res
}

func handleServerStatus(smtpDomain string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		forceRecheck := r.URL.Query().Get("recheck") == "true" || r.URL.Query().Get("recheck") == "1"
		outbound := CheckOutboundPort25(forceRecheck)

		status := struct {
			SMTPDomain           string `json:"smtp_domain"`
			IP                   string `json:"ip"`
			PTR                  string `json:"ptr"`
			PTRFound             bool   `json:"ptr_found"`
			PTRMatches           bool   `json:"ptr_matches"`
			FCrDNSStatus         string `json:"fcrdns_status"` // "ok", "mismatch", "missing_ptr", "missing_a", "unconfigured"
			OutboundPort25Status string `json:"outbound_port_25_status"`
			OutboundPort25Target string `json:"outbound_port_25_target,omitempty"`
			OutboundPort25Error  string `json:"outbound_port_25_error,omitempty"`
		}{
			SMTPDomain:           smtpDomain,
			OutboundPort25Status: outbound.Status,
			OutboundPort25Target: outbound.Target,
			OutboundPort25Error:  outbound.Error,
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
	}
}
