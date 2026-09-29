package model

import (
	"database/sql"
	"time"
)

type CouponRedemption struct {
	ID         int64
	CouponID   int64
	UserID     int64
	OrderID    sql.NullInt64
	RedeemedAt time.Time
}
