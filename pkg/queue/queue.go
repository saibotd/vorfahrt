package queue

import (
	"bytes"
	"crypto/rsa"
	"crypto/tls"
	"errors"
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
}

func (wk *Worker) Start() {
	for {
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
