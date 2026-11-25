package smtp

import (
	"path/filepath"
	"strings"
	"testing"

	"vorfahrt/pkg/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSMTPTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "smtp_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.Account{}, &models.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestSessionMailWithoutACME(t *testing.T) {
	db := setupSMTPTestDB(t)
	acc := models.Account{Domain: "example.com", Username: "user1", Password: "secretpassword"}
	db.Create(&acc)

	// ValidDomains == nil means ACME_FILE is not configured
	s := &Session{db: db, validDomains: nil}
	if err := s.authenticate("user1", "secretpassword"); err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}

	if err := s.Mail("sender@example.com", nil); err != nil {
		t.Fatalf("expected Mail to succeed when ACME_FILE is unset, got: %v", err)
	}
	if err := s.Mail("@example.com", nil); err == nil {
		t.Fatal("expected Mail to reject empty local-part")
	}
	if err := s.Mail("sender@other.com", nil); err == nil {
		t.Fatal("expected Mail to reject untethered domain")
	}
}

func TestSessionMailWithWildcardAndEmptyValidDomains(t *testing.T) {
	db := setupSMTPTestDB(t)
	acc := models.Account{Domain: "sub.example.com", Username: "user2", Password: "secretpassword"}
	db.Create(&acc)

	// Non-nil ValidDomains returning empty should reject
	sEmpty := &Session{db: db, validDomains: func() []string { return nil }}
	if err := sEmpty.authenticate("user2", "secretpassword"); err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if err := sEmpty.Mail("noreply@sub.example.com", nil); err == nil {
		t.Fatal("expected Mail to reject when ACME provider has 0 domains")
	}

	// Wildcard domain should match sub.example.com
	sWild := &Session{db: db, validDomains: func() []string { return []string{"*.example.com"} }}
	if err := sWild.authenticate("user2", "secretpassword"); err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if err := sWild.Mail("noreply@sub.example.com", nil); err != nil {
		t.Fatalf("expected wildcard domain to allow sub.example.com, got: %v", err)
	}
}

func TestSessionRcptAndDataValidation(t *testing.T) {
	db := setupSMTPTestDB(t)
	acc := models.Account{Domain: "example.com", Username: "user3", Password: "secretpassword"}
	db.Create(&acc)

	s := &Session{db: db}
	if err := s.authenticate("user3", "secretpassword"); err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if err := s.Mail("sender@example.com", nil); err != nil {
		t.Fatalf("Mail failed: %v", err)
	}

	for _, bad := range []string{"invalid", "@example.com", "user@", "a@b@c.com"} {
		if err := s.Rcpt(bad, nil); err == nil {
			t.Errorf("expected Rcpt(%q) to fail", bad)
		}
	}

	if err := s.Rcpt("a@dest.com", nil); err != nil {
		t.Fatalf("Rcpt valid 1 failed: %v", err)
	}
	if err := s.Rcpt("b@dest.com", nil); err != nil {
		t.Fatalf("Rcpt valid 2 failed: %v", err)
	}

	if err := s.Data(strings.NewReader("Subject: Hello\r\n\r\nBody")); err != nil {
		t.Fatalf("Data failed: %v", err)
	}

	var count int64
	db.Model(&models.Message{}).Count(&count)
	if count != 2 {
		t.Fatalf("expected 2 queued messages, got %d", count)
	}
}
