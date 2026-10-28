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
	"strings"
	"time"

	"vorfahrt/pkg/models"

	"github.com/emersion/go-msgauth/dkim"
	"gorm.io/gorm"
)

type Worker struct {
	DB         *gorm.DB
	DKIMKey    *rsa.PrivateKey
	Selector   string
	SMTPDomain string
	Retention  time.Duration
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
		var days int
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err == nil && days >= 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	var hours int
	if _, err := fmt.Sscanf(val, "%d", &hours); err == nil && hours >= 0 {
		return time.Duration(hours) * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid retention duration: %s", val)
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

		var messages []models.Message
		wk.DB.Where("status = ?", "queued").Find(&messages)

		for _, msg := range messages {
			msg.Attempts++
			err := wk.processMessage(&msg)
			if err != nil {
				if msg.Attempts >= 5 {
					msg.Status = "failed"
				}
				msg.Error = err.Error()
			} else {
				msg.Status = "sent"
				msg.Data = nil // clear data on success to save space
				msg.Error = ""
			}
			wk.DB.Save(&msg)
		}

		time.Sleep(5 * time.Second)
	}
}

func (wk *Worker) processMessage(msg *models.Message) error {
	parts := strings.Split(msg.To, "@")
	if len(parts) != 2 {
		return errors.New("invalid recipient address")
	}
	domain := parts[1]

	mxs, err := net.LookupMX(domain)
	var addrs []string
	if err == nil && len(mxs) > 0 {
		for _, mx := range mxs {
			host := strings.TrimSuffix(mx.Host, ".")
			addrs = append(addrs, net.JoinHostPort(host, "25"))
		}
	} else {
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
		err := wk.deliverToAddr(addr, msg.From, msg.To, msgData)
		if err == nil {
			return nil
		}
		lastErr = err
		log.Printf("Delivery to %s via %s failed: %v", msg.To, addr, err)
	}

	return lastErr
}

func (wk *Worker) deliverToAddr(addr, from, to string, data []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}

	host, _, _ := net.SplitHostPort(addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()

	helloHost := wk.SMTPDomain
	if helloHost == "" {
		helloHost = "localhost"
	}
	if err := c.Hello(helloHost); err != nil {
		return err
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName: host,
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			log.Printf("STARTTLS with %s warning: %v", host, err)
		}
	}

	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	return c.Quit()
}
