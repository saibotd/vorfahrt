package web

import (
	"encoding/json"
	"net/http"

	"vorfahrt/pkg/models"

	"gorm.io/gorm"
)

func handleMessages(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method == http.MethodGet {
			var msgs []models.Message
			db.Order("created_at desc").Limit(50).Find(&msgs)
			json.NewEncoder(w).Encode(msgs)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}
