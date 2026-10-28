package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"vorfahrt/pkg/models"

	"gorm.io/gorm"
)

type MessageSummary struct {
	ID        uint      `json:"id"`
	Sender    string    `json:"sender"`
	Recipient string    `json:"recipient"`
	Subject   string    `json:"subject"`
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type MessageDetailResponse struct {
	ID          uint              `json:"id"`
	Sender      string            `json:"sender"`
	Recipient   string            `json:"recipient"`
	Subject     string            `json:"subject"`
	Status      string            `json:"status"`
	Attempts    int               `json:"attempts"`
	Error       string            `json:"error"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	HasData     bool              `json:"has_data"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodyPreview string            `json:"body_preview,omitempty"`
	SizeBytes   int               `json:"size_bytes"`
}

func handleMessages(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// If ?id= is specified, return message detail
		idStr := r.URL.Query().Get("id")
		if idStr != "" {
			id, err := strconv.ParseUint(idStr, 10, 32)
			if err != nil {
				http.Error(w, "Invalid message ID", http.StatusBadRequest)
				return
			}

			var msg models.Message
			if err := db.First(&msg, uint(id)).Error; err != nil {
				http.NotFound(w, r)
				return
			}

			detail := formatMessageDetail(&msg, db)
			json.NewEncoder(w).Encode(detail)
			return
		}

		var msgs []models.Message
		db.Order("created_at desc").Limit(50).Find(&msgs)

		summaries := make([]MessageSummary, len(msgs))
		for i := range msgs {
			msg := &msgs[i]
			subj := msg.Subject
			if subj == "" && len(msg.Data) > 0 {
				subj = parseSubjectFromData(msg.Data)
				if subj != "" {
					msg.Subject = subj
					db.Model(&models.Message{}).Where("id = ?", msg.ID).Update("subject", subj)
				}
			}

			summaries[i] = MessageSummary{
				ID:        msg.ID,
				Sender:    msg.From,
				Recipient: msg.To,
				Subject:   subj,
				Status:    msg.Status,
				Attempts:  msg.Attempts,
				Error:     msg.Error,
				CreatedAt: msg.CreatedAt,
				UpdatedAt: msg.UpdatedAt,
			}
		}

		json.NewEncoder(w).Encode(summaries)
	}
}

func handleMessageDetail(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		idStr := r.URL.Query().Get("id")
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			http.Error(w, "Invalid message ID", http.StatusBadRequest)
			return
		}

		var msg models.Message
		if err := db.First(&msg, uint(id)).Error; err != nil {
			http.NotFound(w, r)
			return
		}

		detail := formatMessageDetail(&msg, db)
		json.NewEncoder(w).Encode(detail)
	}
}

func formatMessageDetail(msg *models.Message, db *gorm.DB) MessageDetailResponse {
	subj := msg.Subject
	if subj == "" && len(msg.Data) > 0 {
		subj = parseSubjectFromData(msg.Data)
		if subj != "" && db != nil {
			msg.Subject = subj
			db.Model(&models.Message{}).Where("id = ?", msg.ID).Update("subject", subj)
		}
	}

	resp := MessageDetailResponse{
		ID:        msg.ID,
		Sender:    msg.From,
		Recipient: msg.To,
		Subject:   subj,
		Status:    msg.Status,
		Attempts:  msg.Attempts,
		Error:     msg.Error,
		CreatedAt: msg.CreatedAt,
		UpdatedAt: msg.UpdatedAt,
		HasData:   len(msg.Data) > 0,
		SizeBytes: len(msg.Data),
	}

	if len(msg.Data) > 0 {
		headers, bodyPreview := parseMessageData(msg.Data)
		resp.Headers = headers
		resp.BodyPreview = bodyPreview
	}

	return resp
}

func parseSubjectFromData(data []byte) string {
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

	// Line-by-line fallback
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

func parseMessageData(data []byte) (map[string]string, string) {
	if len(data) == 0 {
		return nil, ""
	}

	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		preview := string(data)
		if len(preview) > 2000 {
			preview = preview[:2000] + "\n...[truncated]..."
		}
		return nil, preview
	}

	headers := make(map[string]string)
	dec := new(mime.WordDecoder)
	for k := range msg.Header {
		val := msg.Header.Get(k)
		if decoded, err := dec.DecodeHeader(val); err == nil {
			val = decoded
		}
		headers[k] = val
	}

	bodyBytes, _ := io.ReadAll(io.LimitReader(msg.Body, 4096))
	bodyStr := string(bodyBytes)
	if len(bodyBytes) == 4096 {
		bodyStr += "\n...[truncated]..."
	}

	return headers, bodyStr
}
