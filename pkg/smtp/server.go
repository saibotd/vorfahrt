package smtp

import (
	"bytes"
	"errors"
	"io"
	"log"
	"mime"
	"net/mail"
	"strings"

	"vorfahrt/pkg/models"

	"github.com/emersion/go-sasl"
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
	account      *models.Account
	from         string
	to           []string
}

func (s *Session) AuthMechanisms() []string {
	return []string{sasl.Plain, sasl.Login}
}

func (s *Session) Auth(mech string) (sasl.Server, error) {
	authenticate := func(username, password string) error {
		var acc models.Account
		if err := s.db.Where("username = ?", username).First(&acc).Error; err != nil {
			return errors.New("Invalid username or password")
		}
		if acc.Password != password {
			return errors.New("Invalid username or password")
		}
		s.username = username
		s.account = &acc
		return nil
	}

	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			return authenticate(username, password)
		}), nil
	case sasl.Login:
		return &loginServer{authenticate: authenticate}, nil
	default:
		return nil, smtp.ErrAuthUnknownMechanism
	}
}

type loginServer struct {
	step         int
	username     string
	authenticate func(username, password string) error
}

func (a *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch a.step {
	case 0:
		a.step++
		if len(response) > 0 {
			a.username = string(response)
			a.step++
			return []byte("Password:"), false, nil
		}
		return []byte("Username:"), false, nil
	case 1:
		a.username = string(response)
		a.step++
		return []byte("Password:"), false, nil
	case 2:
		a.step++
		err = a.authenticate(a.username, string(response))
		return nil, true, err
	default:
		return nil, true, errors.New("sasl: unexpected response")
	}
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
	s.account = &acc
	return nil
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	if s.username == "" || s.account == nil {
		return errors.New("Authentication required")
	}

	parts := strings.Split(from, "@")
	if len(parts) != 2 {
		return errors.New("Invalid sender address")
	}
	senderDomain := parts[1]

	// Verify sender domain matches the account's tethered domain
	if s.account.Domain != "" && !strings.EqualFold(s.account.Domain, senderDomain) {
		return errors.New("Sender domain not allowed for this account")
	}
	
	// Sender Validation
	if s.validDomains != nil {
		domains := s.validDomains()
		if len(domains) > 0 {
			valid := false
			for _, d := range domains {
				if strings.EqualFold(d, senderDomain) {
					valid = true
					break
				}
			}
			if !valid {
				return errors.New("Sender domain not allowed")
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

	subject := parseSubject(data)
	
	for _, to := range s.to {
		msg := models.Message{
			From:    s.from,
			To:      to,
			Subject: subject,
			Data:    data,
			Status:  "queued",
		}
		if err := s.db.Create(&msg).Error; err != nil {
			log.Println("Error saving message:", err)
			return errors.New("Internal server error")
		}
	}

	return nil
}

func parseSubject(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	dec := new(mime.WordDecoder)
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err == nil {
		subj := msg.Header.Get("Subject")
		if subj != "" {
			if decoded, err := dec.DecodeHeader(subj); err == nil {
				return decoded
			}
			return subj
		}
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "subject:") {
			subj := strings.TrimSpace(line[8:])
			if decoded, err := dec.DecodeHeader(subj); err == nil {
				return decoded
			}
			return subj
		}
		if line == "" {
			break
		}
	}
	return ""
}

func (s *Session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *Session) Logout() error {
	return nil
}
