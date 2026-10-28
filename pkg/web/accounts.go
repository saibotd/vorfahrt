package web

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"

	"vorfahrt/pkg/models"

	"gorm.io/gorm"
)

func generatePassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b[i] = charset[num.Int64()]
	}
	return string(b), nil
}

func handleAccounts(db *gorm.DB, validDomains func() []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method == http.MethodGet {
			var accs []models.Account
			db.Order("id desc").Find(&accs)
			json.NewEncoder(w).Encode(accs)
		} else if r.Method == http.MethodPost {
			var req struct {
				Domain   string `json:"domain"`
				Username string `json:"username"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "Invalid request payload", http.StatusBadRequest)
				return
			}

			domain := strings.TrimSpace(req.Domain)
			username := strings.TrimSpace(req.Username)
			if domain == "" || username == "" {
				http.Error(w, "Domain and username are required", http.StatusBadRequest)
				return
			}

			if validDomains != nil {
				domains := validDomains()
				if len(domains) > 0 {
					valid := false
					for _, d := range domains {
						if strings.EqualFold(d, domain) {
							valid = true
							break
						}
					}
					if !valid {
						http.Error(w, "Domain is not in configured sender domains", http.StatusBadRequest)
						return
					}
				}
			}

			pwd, err := generatePassword(24)
			if err != nil {
				http.Error(w, "Failed to generate password", http.StatusInternalServerError)
				return
			}

			acc := models.Account{
				Domain:   domain,
				Username: username,
				Password: pwd,
			}
			if err := db.Create(&acc).Error; err != nil {
				http.Error(w, "Username already exists or database error", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(acc)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handleDeleteAccount(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req struct {
				ID uint `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				db.Delete(&models.Account{}, req.ID)
				w.WriteHeader(http.StatusOK)
			} else {
				http.Error(w, "Invalid request", http.StatusBadRequest)
			}
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}
