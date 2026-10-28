package models

import "time"

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
	Data      []byte    `json:"-"`
	Status    string    `json:"status"` // queued, sent, failed
	Attempts  int       `json:"attempts"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
