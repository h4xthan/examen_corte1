package service

import (
	"context"

	"errors"
	"fmt"
	"strings"
	"time"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

var (
	// ErrCouponInvalid is the single answer for any coupon that is not fit to
	// use: a percentage outside 0-100, a non-positive use limit, a code that is
	// not a code. It is deliberately one error, so an admin cannot use the
	// message to work out which field the server objected to.
	ErrCouponInvalid = errors.New("invalid coupon")
)

// CouponService no longer holds a DiscountLedger.
//
// The ledger was a mutex-guarded map of "coupon currently applied to this
// customer". It is deleted, not patched, for three reasons. It was lost on
// restart, so a customer's discount vanished. It was per process, so two
// instances disagreed about who had spent what. And it was the mechanism of #14:
// because Apply subtracted from the running total, N parallel redemptions of one
// code drove the price towards zero, and the 75ms sleep between the check and
// the write was there to widen the window on purpose.
//
// All three problems come from keeping the state outside the database. A coupon
// redemption is now a row with a unique index on (coupon_id, user_id), and the
// discount is a column on the order.
type CouponService struct {
	coupons store.Coupon
	uses    store.CouponRedemption
}

func NewCouponService(coupons store.Coupon, uses store.CouponRedemption) *CouponService {
	return &CouponService{coupons: coupons, uses: uses}
}

// Preview reports what a code would be worth, without spending it.
//
// The old /coupons/apply endpoint actually spent the coupon and mutated
// in-memory state, and the sleep that made the race possible sat in the middle
// of it. A cart needs to show a price before committing, not after.
func (s *CouponService) Preview(ctx context.Context, userID int64, code string, subtotalCents int64) (*model.Coupon, int64, error) {
	coupon, err := s.coupons.GetCouponByCode(ctx, code)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: no such code", ErrCouponInvalid)
	}

	// A coupon that cannot be used must not be quoted, or the customer sees a
	// discount that checkout will then refuse.
	if coupon.UsedCount >= coupon.MaxUses {
		return nil, 0, fmt.Errorf("%w: exhausted", ErrCouponInvalid)
	}
	if !coupon.ExpiresAt.IsZero() && coupon.ExpiresAt.Before(time.Now()) {
		return nil, 0, fmt.Errorf("%w: expired", ErrCouponInvalid)
	}
	if _, err := s.uses.GetCouponRedemption(ctx, int(coupon.ID), int(userID)); err == nil {
		return nil, 0, fmt.Errorf("%w: already yours", ErrCouponInvalid)
	}

	return coupon, subtotalCents * int64(coupon.DiscountPercent) / 100, nil
}

// ForgetApplied used to drop a customer's pending discount. There is no pending
// state any more, so there is nothing to forget: a customer who changes their
// mind simply does not check out, and the coupon is untouched.
func (s *CouponService) ForgetApplied(ctx context.Context, userID int64) {}

func (s *CouponService) ListCoupons(ctx context.Context) ([]*model.Coupon, error) {
	return s.coupons.GetAllCoupons(ctx)
}

func (s *CouponService) GetCoupon(ctx context.Context, id int) (*model.Coupon, error) {
	return s.coupons.GetCouponByID(ctx, id)
}

func (s *CouponService) GetByCode(ctx context.Context, code string) (*model.Coupon, error) {
	return s.coupons.GetCouponByCode(ctx, code)
}

// validate is the range check the database CHECK constraints also enforce.
//
// Both exist on purpose. The constraint stops a row that should not exist; this
// stops a request and answers with a message instead of a driver error. Neither
// alone is enough: a constraint without a service check produces unhelpful 500s,
// and a service check without a constraint leaves the door open to any other
// writer.
func validateCoupon(c *model.Coupon) error {
	code := strings.TrimSpace(c.Code)
	if code == "" || len(code) > 64 {
		return ErrCouponInvalid
	}
	if c.DiscountPercent < 0 || c.DiscountPercent > 100 {
		return fmt.Errorf("%w: discount_percent must be between 0 and 100", ErrCouponInvalid)
	}
	if c.MaxUses < 1 {
		return fmt.Errorf("%w: max_uses must be at least 1", ErrCouponInvalid)
	}
	if c.UsedCount < 0 || c.UsedCount > c.MaxUses {
		return fmt.Errorf("%w: used_count out of range", ErrCouponInvalid)
	}
	c.Code = code
	return nil
}

// CreateCoupon is mounted behind AdminOnly.
//
// It used to be reachable by any authenticated user, which meant anybody could
// mint a code for themselves with discount_percent set to 100. The route is now
// admin-only and the percentage is range-checked here as well as by the
// constraint.
func (s *CouponService) CreateCoupon(ctx context.Context, coupon *model.Coupon) (*model.Coupon, error) {
	if err := validateCoupon(coupon); err != nil {
		return nil, err
	}
	// A new coupon starts unused. Accepting a caller-supplied used_count would
	// let an admin fabricate redemption history.
	coupon.UsedCount = 0
	return s.coupons.CreateCoupon(ctx, coupon)
}

func (s *CouponService) UpdateCoupon(ctx context.Context, id int, coupon *model.Coupon) (*model.Coupon, error) {
	if err := validateCoupon(coupon); err != nil {
		return nil, err
	}
	return s.coupons.UpdateCoupon(ctx, id, coupon)
}

func (s *CouponService) DeleteCoupon(ctx context.Context, id int) error {
	return s.coupons.DeleteCoupon(ctx, id)
}
