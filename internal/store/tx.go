package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"dvbs/internal/store/db"
)

type Querier = db.Querier

var ErrNoRows = sql.ErrNoRows

type Runner struct {
	db *sql.DB
}

func NewRunner(db *sql.DB) *Runner {
	return &Runner{db: db}
}

type Queriers struct {
	Q              Querier
	Books          Store
	Coupons        Coupon
	CouponUses     CouponRedemption
	Orders         Order
	OrderItems     OrderItem
	Users          User
	Reviews        Review
	PaymentMethods PaymentMethod
	Addresses      Address
	ResetTokens    PasswordResetToken
}

func NewQueriers(q Querier) Queriers {
	return Queriers{
		Q:              q,
		Books:          NewBookStore(q),
		Coupons:        NewCouponStore(q),
		CouponUses:     NewCouponRedemptionStore(q),
		Orders:         NewOrderStore(q),
		OrderItems:     NewOrderItemStore(q),
		Users:          NewUserStore(q),
		Reviews:        NewReviewStore(q),
		PaymentMethods: NewPaymentMethodStore(q),
		Addresses:      NewAddressStore(q),
		ResetTokens:    NewPasswordResetTokenStore(q),
	}
}

func (r *Runner) WithinTx(ctx context.Context, fn func(Queriers) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(NewQueriers(db.New(tx))); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func ErrNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	return err
}
