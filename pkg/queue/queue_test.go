package queue

import (
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
