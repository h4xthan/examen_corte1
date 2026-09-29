package model

type Address struct {
	ID     int64  `json:"id,string"`
	UserID int64  `json:"user_id,string"`
	Street string `json:"street"`
	City   string `json:"city"`
	Zip    string `json:"zip"`
}
