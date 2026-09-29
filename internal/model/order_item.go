package model

import "time"

type OrderItem struct {
	ID       int64 `json:"id,string"`
	OrderID  int64 `json:"order_id,string"`
	BookID   int64 `json:"book_id,string"`
	Quantity int   `json:"quantity"`
	// UnitPriceCents is the catalogue price at the moment the line was added,
	// in cents. It was `unit_price` as a float and, worse, it was taken straight
	// from the request body, so a client could name its own price.
	UnitPriceCents int64     `json:"unit_price_cents"`
	CreatedAt      time.Time `json:"created_at"`
}
