package models

import (
	"bytes"
	"mime"
	"net/mail"
	"strings"
	"time"
)

type Account struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Domain    string    `json:"domain"`
	Username  string    `gorm:"uniqueIndex" json:"username"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	From      string    `json:"sender"`
	To        string    `json:"recipient"`
	Subject   string    `json:"subject"`
	Data      []byte    `json:"-"`
	Status    string    `json:"status"` // queued, sent, failed
	Attempts  int       `json:"attempts"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ParseSubject extracts and MIME-decodes the Subject header from raw email data.
func ParseSubject(data []byte) string {
	data = bytes.TrimLeft(data, "\r\n ")
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

	// Line-by-line fallback supporting folded headers
	lines := strings.Split(string(data), "\n")
	var currentHeader string
	var headerVal strings.Builder

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if currentHeader == "subject" {
				headerVal.WriteString(" ")
				headerVal.WriteString(strings.TrimSpace(line))
			}
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			currentHeader = strings.ToLower(strings.TrimSpace(parts[0]))
			if currentHeader == "subject" {
				headerVal.WriteString(strings.TrimSpace(parts[1]))
			}
		} else {
			currentHeader = ""
		}
	}

	subj := headerVal.String()
	if subj != "" {
		if decoded, err := dec.DecodeHeader(subj); err == nil {
			return decoded
		}
		return subj
	}
	return ""
}
