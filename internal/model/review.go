package model

import "time"

type Review struct {
	ID        int64     `json:"id,string"`
	BookID    int64     `json:"book_id,string"`
	UserID    int64     `json:"user_id,string"`
	Rating    int       `json:"rating"`
	Comment   string    `json:"comment"`
	ImageURL  string    `json:"image_url"`
	CreatedAt time.Time `json:"created_at"`
}
