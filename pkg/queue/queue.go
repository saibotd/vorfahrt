package queue

import (
	"bytes"
	"crypto/rsa"
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
	DB       *gorm.DB
	DKIMKey  *rsa.PrivateKey
	Selector string
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
		return net.ErrClosed // dummy error, should use better one
	}
	domain := parts[1]

	mxs, err := net.LookupMX(domain)
	var addr string
	if err != nil || len(mxs) == 0 {
		// Fallback to A record
		addr = domain + ":25"
	} else {
		// Try the first MX
		addr = mxs[0].Host + ":25"
	}

	log.Printf("Delivering to %s via %s", msg.To, addr)
	
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

	// Basic sendmail without auth (server-to-server)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	
	c, err := smtp.NewClient(conn, strings.Split(addr, ":")[0])
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()

	if err := c.Mail(msg.From); err != nil {
		return err
	}
	if err := c.Rcpt(msg.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msgData); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	return c.Quit()
}
