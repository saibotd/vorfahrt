package queue

import (
	"bytes"
	"crypto/rsa"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vorfahrt/pkg/models"

	"github.com/emersion/go-msgauth/dkim"
	"gorm.io/gorm"
)

type Worker struct {
	DB          *gorm.DB
	DKIMKey     *rsa.PrivateKey
	Selector    string
	SMTPDomain  string
	Retention   time.Duration
	RetryDelay  func(attempts int) time.Duration
	DeliverFunc func(addr, from, to string, data []byte) error
	LookupMX    func(domain string) ([]*net.MX, error)
}

// ParseRetention parses a duration for queue message retention (e.g. "24h", "2d", "168h", "24").
// An empty string defaults to 24 hours. "0", "disable", or "none" disables retention cleanup (returns 0).
func ParseRetention(val string) (time.Duration, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return 24 * time.Hour, nil
	}
	if val == "0" || strings.EqualFold(val, "disable") || strings.EqualFold(val, "disabled") || strings.EqualFold(val, "none") || strings.EqualFold(val, "false") {
		return 0, nil
	}
	if d, err := time.ParseDuration(val); err == nil {
		if d < 0 {
			return 0, errors.New("retention duration cannot be negative")
		}
		return d, nil
	}
	if strings.HasSuffix(strings.ToLower(val), "d") {
		daysStr := val[:len(val)-1]
		if days, err := strconv.Atoi(daysStr); err == nil && days >= 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	if hours, err := strconv.Atoi(val); err == nil && hours >= 0 {
		return time.Duration(hours) * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid retention duration: %s", val)
}

var (
	smtp5xxRegex = regexp.MustCompile(`(?i)^(?:smtp:\s*)?5\d{2}(?:[ \-\r\n]|$)`)
	smtp4xxRegex = regexp.MustCompile(`(?i)^(?:smtp:\s*)?4\d{2}(?:[ \-\r\n]|$)`)
)

// DefaultRetryDelay returns the backoff delay for the given attempt count.
// Attempt 1 -> 1m, Attempt 2 -> 2m, Attempt 3 -> 4m, Attempt 4 -> 8m, Attempt 5+ -> 15m.
func DefaultRetryDelay(attempts int) time.Duration {
	switch attempts {
	case 1:
		return 1 * time.Minute
	case 2:
		return 2 * time.Minute
	case 3:
		return 4 * time.Minute
	case 4:
		return 8 * time.Minute
	default:
		return 15 * time.Minute
	}
}

func (wk *Worker) getRetryDelay(attempts int) time.Duration {
	if wk.RetryDelay != nil {
		return wk.RetryDelay(attempts)
	}
	return DefaultRetryDelay(attempts)
}

// IsFatalError determines if an error represents a permanent delivery failure that should not be retried.
func IsFatalError(err error) bool {
	if err == nil {
		return false
	}

	// 1. SMTP protocol error codes via textproto.Error
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		if tpErr.Code >= 500 && tpErr.Code <= 599 {
			return true
		}
		if tpErr.Code >= 400 && tpErr.Code <= 499 {
			return false
		}
	}

	// 2. DNS errors: NXDOMAIN / no such host is fatal, while timeouts or temporary errors are not
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return true
		}
		if !dnsErr.IsTimeout && !dnsErr.Temporary() && strings.Contains(strings.ToLower(dnsErr.Err), "no such host") {
			return true
		}
	}

	// 3. String-based matching for recipient syntax, Null MX, or 5xx codes
	errMsg := strings.TrimSpace(err.Error())
	lowerErr := strings.ToLower(errMsg)
	if strings.Contains(lowerErr, "invalid recipient") ||
		strings.Contains(lowerErr, "null mx") ||
		strings.Contains(lowerErr, "does not accept mail") {
		return true
	}

	// If 4xx is present at the start of the error string, treat as temporary
	if smtp4xxRegex.MatchString(errMsg) {
		return false
	}

	if smtp5xxRegex.MatchString(errMsg) {
		return true
	}

	return false
}

func (wk *Worker) Start() {
	var lastCleanup time.Time
	for {
		if wk.Retention > 0 && time.Since(lastCleanup) >= 1*time.Minute {
			cutoff := time.Now().Add(-wk.Retention)
			if res := wk.DB.Where("created_at < ?", cutoff).Delete(&models.Message{}); res.Error != nil {
				log.Println("Error cleaning up expired queue items:", res.Error)
			} else if res.RowsAffected > 0 {
				log.Printf("Cleaned up %d expired queue item(s) older than %s", res.RowsAffected, wk.Retention)
			}
			lastCleanup = time.Now()
		}

		wk.ProcessOnce()

		time.Sleep(5 * time.Second)
	}
}

// ProcessOnce scans the queue for messages due for processing and attempts delivery.
func (wk *Worker) ProcessOnce() {
	var messages []models.Message
	now := time.Now().UTC()
	wk.DB.Where("status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", "queued", now).Find(&messages)

	for _, msg := range messages {
		if msg.Subject == "" && len(msg.Data) > 0 {
			msg.Subject = models.ParseSubject(msg.Data)
		}
		msg.Attempts++
		err := wk.processMessage(&msg)
		if err != nil {
			msg.Error = err.Error()
			if IsFatalError(err) || msg.Attempts >= 5 {
				msg.Status = "failed"
				msg.NextRetryAt = nil
			} else {
				delay := wk.getRetryDelay(msg.Attempts)
				nextRetry := time.Now().UTC().Add(delay)
				msg.NextRetryAt = &nextRetry
			}
		} else {
			msg.Status = "sent"
			msg.Data = nil // clear data on success to save space
			msg.Error = ""
			msg.NextRetryAt = nil
		}
		if err := wk.DB.Save(&msg).Error; err != nil {
			log.Printf("Error saving message %d: %v", msg.ID, err)
		}
	}
}

func (wk *Worker) processMessage(msg *models.Message) error {
	parts := strings.Split(msg.To, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("invalid recipient address")
	}
	domain := parts[1]

	lookupMX := net.LookupMX
	if wk.LookupMX != nil {
		lookupMX = wk.LookupMX
	}

	var addrs []string
	mxs, mxErr := lookupMX(domain)
	if mxErr != nil {
		var dnsErr *net.DNSError
		if !errors.As(mxErr, &dnsErr) || !dnsErr.IsNotFound {
			return mxErr
		}
	} else if len(mxs) > 0 {
		// RFC 7505: a single MX record with host "." (Null MX) means the domain does not accept mail.
		if len(mxs) == 1 && strings.TrimSuffix(mxs[0].Host, ".") == "" {
			return errors.New("domain does not accept mail (Null MX)")
		}
		for _, mx := range mxs {
			host := strings.TrimSuffix(mx.Host, ".")
			if host == "" {
				continue
			}
			addrs = append(addrs, net.JoinHostPort(host, "25"))
		}
		if len(addrs) == 0 {
			return errors.New("domain does not accept mail (Null MX)")
		}
	}

	if len(addrs) == 0 {
		addrs = append(addrs, net.JoinHostPort(domain, "25"))
	}

	var msgData []byte = msg.Data
	if wk.DKIMKey != nil {
		senderDomain := strings.Split(msg.From, "@")
		if len(senderDomain) == 2 {
			options := &dkim.SignOptions{
				Domain:   senderDomain[1],
				Selector: wk.Selector,
				Signer:   wk.DKIMKey,
			}

			var signedBuf bytes.Buffer
			if err := dkim.Sign(&signedBuf, bytes.NewReader(msg.Data), options); err == nil {
				msgData = signedBuf.Bytes()
			} else {
				log.Println("DKIM signing failed:", err)
			}
		}
	}

	var lastErr error
	for _, addr := range addrs {
		log.Printf("Delivering to %s via %s", msg.To, addr)
		var err error
		if wk.DeliverFunc != nil {
			err = wk.DeliverFunc(addr, msg.From, msg.To, msgData)
		} else {
			err = wk.deliverToAddr(addr, msg.From, msg.To, msgData)
		}
		if err == nil {
			return nil
		}
		lastErr = err
		log.Printf("Delivery to %s via %s failed: %v", msg.To, addr, err)
		if IsFatalError(err) {
			break
		}
	}

	return lastErr
}

func (wk *Worker) deliverToAddr(addr, from, to string, data []byte) error {
	startTLSFailed, err := wk.attemptDelivery(addr, from, to, data, false)
	if err != nil && startTLSFailed {
		host, _, _ := net.SplitHostPort(addr)
		log.Printf("STARTTLS with %s failed (%v); retrying with opportunistic TLS on a new connection", host, err)
		_, err = wk.attemptDelivery(addr, from, to, data, true)
	}
	return err
}

func (wk *Worker) attemptDelivery(addr, from, to string, data []byte, insecureTLS bool) (startTLSFailed bool, err error) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return false, err
	}
	// Bound the total duration of the SMTP session so a stalled MX cannot freeze the queue worker.
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	host, _, _ := net.SplitHostPort(addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return false, err
	}
	defer c.Close()

	helloHost := wk.SMTPDomain
	if helloHost == "" {
		helloHost = "localhost"
	}
	if err := c.Hello(helloHost); err != nil {
		return false, err
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: insecureTLS,
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return true, err
		}
	}

	if err := c.Mail(from); err != nil {
		return false, err
	}
	if err := c.Rcpt(to); err != nil {
		return false, err
	}
	w, err := c.Data()
	if err != nil {
		return false, err
	}
	if _, err := w.Write(data); err != nil {
		return false, err
	}
	if err := w.Close(); err != nil {
		return false, err
	}

	// The remote server has already accepted the message (250 OK).
	// Ignore QUIT errors so a server closing early does not trigger duplicate retries.
	if err := c.Quit(); err != nil {
		log.Printf("SMTP QUIT with %s warning: %v", host, err)
	}
	return false, nil
}
