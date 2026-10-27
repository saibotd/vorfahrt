package web

import (
	"encoding/json"
	"net/http"

	"vorfahrt/pkg/models"

	"gorm.io/gorm"
)

func RegisterHandlers(mux *http.ServeMux, db *gorm.DB) {
	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			var accs []models.Account
			db.Find(&accs)
			json.NewEncoder(w).Encode(accs)
		} else if r.Method == http.MethodPost {
			var acc models.Account
			if err := json.NewDecoder(r.Body).Decode(&acc); err == nil {
				db.Create(&acc)
				json.NewEncoder(w).Encode(acc)
			}
		}
	})

	mux.HandleFunc("/api/accounts/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req struct { ID uint `json:"id"` }
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				db.Delete(&models.Account{}, req.ID)
				w.WriteHeader(http.StatusOK)
			}
		}
	})

	mux.HandleFunc("/api/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			var msgs []models.Message
			db.Order("created_at desc").Limit(50).Find(&msgs)
			json.NewEncoder(w).Encode(msgs)
		}
	})
}
