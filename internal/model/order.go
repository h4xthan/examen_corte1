package model

import (
	"database/sql"
	"time"
)

type Order struct {
	ID        int64     `json:"id,string"`
	UserID    int64     `json:"user_id,string"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`

	// The three money columns, all integer cents. They are integers because
	// binary floating point cannot represent most decimal fractions exactly, and
	// a book shop that rounds a customer's invoice by a cent is a book shop with
	// an accounting bug.
	//
	// The old field was a float64 decoded from NUMERIC(10,2), which checkout then
	// truncated with int64(total). A 19.99 order could be recorded as 19 cents.
	SubtotalCents int64 `json:"subtotal_cents"`
	DiscountCents int64 `json:"discount_cents"`
	TotalCents    int64 `json:"total_cents"`

	// CouponID is the coupon this order actually used, if any. It replaces
	// internal/service/discount_ledger.go, which held the same fact in a map in
	// memory: lost on restart, invisible to a second instance, and the reason a
	// discount could be compounded onto an already-discounted price (#14).
	CouponID sql.NullInt64 `json:"coupon_id,omitempty"`
}
