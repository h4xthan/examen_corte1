package model

import "time"

type Coupon struct {
	ID              int64     `json:"id,string"`
	Code            string    `json:"code"`
	DiscountPercent int       `json:"discount_percent"`
	MaxUses         int       `json:"max_uses"`
	ExpiresAt       time.Time `json:"expires_at"`
	UsedCount       int       `json:"used_count"`
}
