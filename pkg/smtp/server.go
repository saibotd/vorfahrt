package smtp

import (
	"errors"
	"io"
	"log"

	"strings"

	"vorfahrt/pkg/models"

	"github.com/emersion/go-smtp"
	"gorm.io/gorm"
)

type Backend struct {
	DB           *gorm.DB
	ValidDomains func() []string
}

func (bkd *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &Session{
		db:           bkd.DB,
		validDomains: bkd.ValidDomains,
	}, nil
}

type Session struct {
	db           *gorm.DB
	validDomains func() []string
	username     string
	from         string
	to           []string
}

func (s *Session) AuthPlain(username, password string) error {
	var acc models.Account
	if err := s.db.Where("username = ?", username).First(&acc).Error; err != nil {
		return errors.New("Invalid username or password")
	}
	if acc.Password != password {
		return errors.New("Invalid username or password")
	}
	s.username = username
	return nil
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	if s.username == "" {
		return errors.New("Authentication required")
	}
	
	// Sender Validation
	if s.validDomains != nil {
		domains := s.validDomains()
		if len(domains) > 0 {
			parts := strings.Split(from, "@")
			if len(parts) == 2 {
				domain := parts[1]
				valid := false
				for _, d := range domains {
					if strings.EqualFold(d, domain) {
						valid = true
						break
					}
				}
				if !valid {
					return errors.New("Sender domain not allowed")
				}
			} else {
				return errors.New("Invalid sender address")
			}
		} else {
			// acme.json exists but no domains configured yet
			return errors.New("No valid sender domains configured yet")
		}
	}

	s.from = from
	return nil
}

func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *Session) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	
	for _, to := range s.to {
		msg := models.Message{
			From:   s.from,
			To:     to,
			Data:   data,
			Status: "queued",
		}
		if err := s.db.Create(&msg).Error; err != nil {
			log.Println("Error saving message:", err)
			return errors.New("Internal server error")
		}
	}

	return nil
}

func (s *Session) Reset() {}

func (s *Session) Logout() error {
	return nil
}
