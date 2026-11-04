package queue

import (
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"path/filepath"
	"testing"
	"time"

	"vorfahrt/pkg/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestParseRetention(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"", 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"48h", 48 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"12", 12 * time.Hour, false},
		{"0", 0, false},
		{"disable", 0, false},
		{"disabled", 0, false},
		{"none", 0, false},
		{"false", 0, false},
		{"-5h", 0, true},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		got, err := ParseRetention(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseRetention(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.expected {
			t.Errorf("ParseRetention(%q) = %v, want = %v", tt.input, got, tt.expected)
		}
	}
}

func TestQueueRetentionCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test DB: %v", err)
	}
	db.AutoMigrate(&models.Message{})

	now := time.Now()
	// Insert one old message (30h ago)
	oldMsg := models.Message{
		From:      "old@example.com",
		To:        "test@example.com",
		Status:    "sent",
		CreatedAt: now.Add(-30 * time.Hour),
		UpdatedAt: now.Add(-30 * time.Hour),
	}
	db.Create(&oldMsg)

	// Insert one recent message (2h ago)
	newMsg := models.Message{
		From:      "new@example.com",
		To:        "test@example.com",
		Status:    "sent",
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
	}
	db.Create(&newMsg)

	retention := 24 * time.Hour
	cutoff := now.Add(-retention)
	res := db.Where("created_at < ?", cutoff).Delete(&models.Message{})
	if res.Error != nil {
		t.Fatalf("Delete failed: %v", res.Error)
	}
	if res.RowsAffected != 1 {
		t.Errorf("Expected 1 row deleted, got %d", res.RowsAffected)
	}

	var count int64
	db.Model(&models.Message{}).Count(&count)
	if count != 1 {
		t.Errorf("Expected 1 message remaining in DB, got %d", count)
	}

	var remaining models.Message
	db.First(&remaining)
	if remaining.From != "new@example.com" {
		t.Errorf("Expected new message to remain, got from: %s", remaining.From)
	}
}

func TestIsFatalError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		isFatal bool
	}{
		{"nil error", nil, false},
		{"textproto 550", &textproto.Error{Code: 550, Msg: "User unknown"}, true},
		{"textproto 554", &textproto.Error{Code: 554, Msg: "Transaction failed"}, true},
		{"textproto 501", &textproto.Error{Code: 501, Msg: "Bad syntax"}, true},
		{"textproto 500", &textproto.Error{Code: 500, Msg: "Syntax error"}, true},
		{"wrapped textproto 550", fmt.Errorf("delivery failed: %w", &textproto.Error{Code: 550, Msg: "User unknown"}), true},
		{"textproto 451", &textproto.Error{Code: 451, Msg: "Greylisted"}, false},
		{"textproto 421", &textproto.Error{Code: 421, Msg: "Service temporarily unavailable"}, false},
		{"wrapped textproto 451", fmt.Errorf("delivery failed: %w", &textproto.Error{Code: 451, Msg: "Greylisted"}), false},
		{"string 550 prefix", errors.New("550 5.1.1 User unknown"), true},
		{"string 554 prefix", errors.New("554 Relay rejected"), true},
		{"string with 550", errors.New("smtp: 550 mailbox not found"), true},
		{"string 451 prefix", errors.New("451 4.7.1 Greylisted, please try again"), false},
		{"string 421 prefix", errors.New("421 server busy"), false},
		{"invalid recipient address", errors.New("invalid recipient address"), true},
		{"DNS not found", &net.DNSError{IsNotFound: true, Err: "no such host"}, true},
		{"DNS no such host string", &net.DNSError{Err: "no such host"}, true},
		{"DNS timeout", &net.DNSError{IsTimeout: true, Err: "i/o timeout"}, false},
		{"network timeout", errors.New("dial tcp 1.2.3.4:25: i/o timeout"), false},
		{"connection refused port 25", errors.New("dial tcp 1.2.3.4:25: connect: connection refused"), false},
		{"connection refused port 587", errors.New("dial tcp 1.2.3.4:587: connect: connection refused"), false},
		{"read reset by peer", errors.New("read tcp 1.2.3.4:55000: connection reset by peer"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsFatalError(tt.err)
			if got != tt.isFatal {
				t.Errorf("IsFatalError(%v) = %v, want %v", tt.err, got, tt.isFatal)
			}
		})
	}
}

func setupTestDB(t *testing.T) *gorm.DB {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "queue_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test DB: %v", err)
	}
	db.AutoMigrate(&models.Message{})
	return db
}

func TestQueueFatalErrorNoRetry(t *testing.T) {
	db := setupTestDB(t)

	msg := models.Message{
		From:    "sender@example.com",
		To:      "recipient@example.com",
		Subject: "Test 5xx",
		Data:    []byte("Subject: Test 5xx\r\n\r\nHello"),
		Status:  "queued",
	}
	db.Create(&msg)

	deliverCalls := 0
	worker := &Worker{
		DB: db,
		LookupMX: func(domain string) ([]*net.MX, error) {
			return []*net.MX{{Host: "mail.example.com", Pref: 10}}, nil
		},
		DeliverFunc: func(addr, from, to string, data []byte) error {
			deliverCalls++
			return &textproto.Error{Code: 550, Msg: "5.1.1 User unknown"}
		},
	}

	// First pass
	worker.ProcessOnce()

	var updated models.Message
	db.First(&updated, msg.ID)
	if updated.Status != "failed" {
		t.Errorf("Expected status to be 'failed', got %q", updated.Status)
	}
	if updated.Attempts != 1 {
		t.Errorf("Expected attempts to be 1, got %d", updated.Attempts)
	}
	if updated.NextRetryAt != nil {
		t.Errorf("Expected NextRetryAt to be nil for fatal error, got %v", updated.NextRetryAt)
	}
	if deliverCalls != 1 {
		t.Errorf("Expected 1 delivery attempt, got %d", deliverCalls)
	}

	// Second pass: should NOT retry
	worker.ProcessOnce()
	db.First(&updated, msg.ID)
	if deliverCalls != 1 {
		t.Errorf("Expected deliverCalls to still be 1 after second pass, got %d", deliverCalls)
	}
	if updated.Attempts != 1 {
		t.Errorf("Expected attempts to remain 1, got %d", updated.Attempts)
	}
}

func TestQueueInvalidRecipientNoRetry(t *testing.T) {
	db := setupTestDB(t)

	msg := models.Message{
		From:    "sender@example.com",
		To:      "invalid-recipient-without-at",
		Subject: "Test Bad To",
		Data:    []byte("Subject: Test Bad To\r\n\r\nHello"),
		Status:  "queued",
	}
	db.Create(&msg)

	deliverCalls := 0
	worker := &Worker{
		DB: db,
		DeliverFunc: func(addr, from, to string, data []byte) error {
			deliverCalls++
			return nil
		},
	}

	worker.ProcessOnce()

	var updated models.Message
	db.First(&updated, msg.ID)
	if updated.Status != "failed" {
		t.Errorf("Expected status to be 'failed', got %q", updated.Status)
	}
	if updated.Attempts != 1 {
		t.Errorf("Expected attempts to be 1, got %d", updated.Attempts)
	}
	if updated.NextRetryAt != nil {
		t.Errorf("Expected NextRetryAt to be nil, got %v", updated.NextRetryAt)
	}
	if deliverCalls != 0 {
		t.Errorf("Expected 0 deliver calls for invalid recipient, got %d", deliverCalls)
	}
}

func TestQueueNonFatalRetrySpacing(t *testing.T) {
	db := setupTestDB(t)

	msg := models.Message{
		From:    "sender@example.com",
		To:      "user@example.com",
		Subject: "Test 4xx Retry",
		Data:    []byte("Subject: Test 4xx Retry\r\n\r\nHello"),
		Status:  "queued",
	}
	db.Create(&msg)

	worker := &Worker{
		DB: db,
		LookupMX: func(domain string) ([]*net.MX, error) {
			return []*net.MX{{Host: "mail.example.com", Pref: 10}}, nil
		},
		DeliverFunc: func(addr, from, to string, data []byte) error {
			return &textproto.Error{Code: 451, Msg: "Greylisted, try again later"}
		},
	}

	before := time.Now().UTC()
	worker.ProcessOnce()

	var updated models.Message
	db.First(&updated, msg.ID)
	if updated.Status != "queued" {
		t.Fatalf("Expected status 'queued', got %q", updated.Status)
	}
	if updated.Attempts != 1 {
		t.Fatalf("Expected attempts = 1, got %d", updated.Attempts)
	}
	if updated.NextRetryAt == nil {
		t.Fatalf("Expected NextRetryAt to be set")
	}

	// Verify retry is spaced out by approximately 1 minute (DefaultRetryDelay for attempt 1)
	expectedDelay := 1 * time.Minute
	diff := updated.NextRetryAt.Sub(before)
	if diff < expectedDelay-5*time.Second || diff > expectedDelay+5*time.Second {
		t.Errorf("Expected NextRetryAt ~1m from now, got diff %v", diff)
	}

	// Running ProcessOnce immediately should NOT process the message because NextRetryAt is in the future
	worker.ProcessOnce()
	db.First(&updated, msg.ID)
	if updated.Attempts != 1 {
		t.Errorf("Message was processed prematurely! Attempts = %d, expected 1", updated.Attempts)
	}

	// Now artificially advance NextRetryAt into the past
	past := time.Now().UTC().Add(-10 * time.Second)
	updated.NextRetryAt = &past
	db.Save(&updated)

	// Now ProcessOnce should pick it up for attempt 2
	worker.ProcessOnce()
	db.First(&updated, msg.ID)
	if updated.Attempts != 2 {
		t.Errorf("Expected attempts = 2 after retry, got %d", updated.Attempts)
	}
	if updated.Status != "queued" {
		t.Errorf("Expected status to still be 'queued', got %q", updated.Status)
	}

	// Verify attempt 2 is spaced out by ~2 minutes
	diff2 := updated.NextRetryAt.Sub(time.Now().UTC())
	if diff2 < 2*time.Minute-5*time.Second || diff2 > 2*time.Minute+5*time.Second {
		t.Errorf("Expected NextRetryAt ~2m from now for attempt 2, got diff %v", diff2)
	}
}

func TestQueueMaxAttemptsExceeded(t *testing.T) {
	db := setupTestDB(t)

	msg := models.Message{
		From:     "sender@example.com",
		To:       "user@example.com",
		Subject:  "Test Max Attempts",
		Data:     []byte("Subject: Test Max Attempts\r\n\r\nHello"),
		Status:   "queued",
		Attempts: 4, // Next attempt will be attempt 5
	}
	db.Create(&msg)

	worker := &Worker{
		DB: db,
		LookupMX: func(domain string) ([]*net.MX, error) {
			return []*net.MX{{Host: "mail.example.com", Pref: 10}}, nil
		},
		DeliverFunc: func(addr, from, to string, data []byte) error {
			return &textproto.Error{Code: 451, Msg: "Still greylisted"}
		},
	}

	worker.ProcessOnce()

	var updated models.Message
	db.First(&updated, msg.ID)
	if updated.Attempts != 5 {
		t.Errorf("Expected attempts = 5, got %d", updated.Attempts)
	}
	if updated.Status != "failed" {
		t.Errorf("Expected status = 'failed' after 5 attempts, got %q", updated.Status)
	}
	if updated.NextRetryAt != nil {
		t.Errorf("Expected NextRetryAt to be nil, got %v", updated.NextRetryAt)
	}
}

func TestQueueFatalStopsMXLoop(t *testing.T) {
	db := setupTestDB(t)

	msg := models.Message{
		From:    "sender@example.com",
		To:      "user@example.com",
		Subject: "Test MX Loop Abort",
		Data:    []byte("Subject: Test MX Loop Abort\r\n\r\nHello"),
		Status:  "queued",
	}
	db.Create(&msg)

	var attemptedAddrs []string
	worker := &Worker{
		DB: db,
		LookupMX: func(domain string) ([]*net.MX, error) {
			return []*net.MX{
				{Host: "mx1.example.com", Pref: 10},
				{Host: "mx2.example.com", Pref: 20},
			}, nil
		},
		DeliverFunc: func(addr, from, to string, data []byte) error {
			attemptedAddrs = append(attemptedAddrs, addr)
			return &textproto.Error{Code: 550, Msg: "User unknown"}
		},
	}

	worker.ProcessOnce()

	if len(attemptedAddrs) != 1 {
		t.Errorf("Expected only 1 MX attempted on fatal error, got %d (%v)", len(attemptedAddrs), attemptedAddrs)
	}
	if len(attemptedAddrs) > 0 && attemptedAddrs[0] != "mx1.example.com:25" {
		t.Errorf("Expected mx1.example.com:25, got %s", attemptedAddrs[0])
	}
}

func TestQueueSuccessClearsDataAndNextRetry(t *testing.T) {
	db := setupTestDB(t)

	msg := models.Message{
		From:    "sender@example.com",
		To:      "user@example.com",
		Subject: "Test Success",
		Data:    []byte("Subject: Test Success\r\n\r\nHello world"),
		Status:  "queued",
	}
	db.Create(&msg)

	worker := &Worker{
		DB: db,
		LookupMX: func(domain string) ([]*net.MX, error) {
			return []*net.MX{{Host: "mail.example.com", Pref: 10}}, nil
		},
		DeliverFunc: func(addr, from, to string, data []byte) error {
			return nil
		},
	}

	worker.ProcessOnce()

	var updated models.Message
	db.First(&updated, msg.ID)
	if updated.Status != "sent" {
		t.Errorf("Expected status = 'sent', got %q", updated.Status)
	}
	if len(updated.Data) != 0 {
		t.Errorf("Expected Data to be cleared on success, got %d bytes", len(updated.Data))
	}
	if updated.NextRetryAt != nil {
		t.Errorf("Expected NextRetryAt to be nil on success, got %v", updated.NextRetryAt)
	}
	if updated.Error != "" {
		t.Errorf("Expected Error to be empty, got %q", updated.Error)
	}
}
