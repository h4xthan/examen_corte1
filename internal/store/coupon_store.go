package store

import (
	"context"
	"database/sql"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type Coupon interface {
	GetAllCoupons(ctx context.Context) ([]*model.Coupon, error)
	GetCouponByID(ctx context.Context, id int) (*model.Coupon, error)
	GetCouponByCode(ctx context.Context, code string) (*model.Coupon, error)
	CreateCoupon(ctx context.Context, coupon *model.Coupon) (*model.Coupon, error)
	UpdateCoupon(ctx context.Context, id int, coupon *model.Coupon) (*model.Coupon, error)
	DeleteCoupon(ctx context.Context, id int) error
	RedeemCoupon(ctx context.Context, id int) (int64, error)
	GiveBackCouponUse(ctx context.Context, id int) error
	CountCoupons(ctx context.Context) (int64, error)
}

type CouponStore struct {
	q db.Querier
}

func NewCouponStore(q db.Querier) Coupon {
	return &CouponStore{q: q}
}

func couponFromDB(r db.Coupon) *model.Coupon {
	return &model.Coupon{
		ID:              int64(r.ID),
		Code:            r.Code,
		DiscountPercent: int(r.DiscountPercent),
		MaxUses:         int(r.MaxUses),
		ExpiresAt:       r.ExpiresAt,
		UsedCount:       int(r.UsedCount),
	}
}

func (s *CouponStore) GetAllCoupons(ctx context.Context) ([]*model.Coupon, error) {
	rows, err := s.q.GetAllCoupons(ctx)
	if err != nil {
		return nil, err
	}
	coupons := make([]*model.Coupon, 0, len(rows))
	for _, r := range rows {
		coupons = append(coupons, couponFromDB(r))
	}
	return coupons, nil
}

func (s *CouponStore) GetCouponByID(ctx context.Context, id int) (*model.Coupon, error) {
	r, err := s.q.GetCouponByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return couponFromDB(r), nil
}

func (s *CouponStore) GetCouponByCode(ctx context.Context, code string) (*model.Coupon, error) {
	r, err := s.q.GetCouponByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	return couponFromDB(r), nil
}

func (s *CouponStore) CreateCoupon(ctx context.Context, coupon *model.Coupon) (*model.Coupon, error) {
	res, err := s.q.CreateCoupon(ctx, db.CreateCouponParams{
		Code:            coupon.Code,
		DiscountPercent: int32(coupon.DiscountPercent),
		MaxUses:         int32(coupon.MaxUses),
		ExpiresAt:       coupon.ExpiresAt,
		UsedCount:       int32(coupon.UsedCount),
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetCouponByID(ctx, int(id))
}

func (s *CouponStore) UpdateCoupon(ctx context.Context, id int, coupon *model.Coupon) (*model.Coupon, error) {
	if err := s.q.UpdateCoupon(ctx, db.UpdateCouponParams{
		ID:              int64(id),
		Code:            coupon.Code,
		DiscountPercent: int32(coupon.DiscountPercent),
		MaxUses:         int32(coupon.MaxUses),
		ExpiresAt:       coupon.ExpiresAt,
		UsedCount:       int32(coupon.UsedCount),
	}); err != nil {
		return nil, err
	}
	return s.GetCouponByID(ctx, id)
}

// DeleteCoupon removes the coupon, or reports that there was nothing to remove.
//
// The query returns the row count because a DELETE that matches nothing is not
// an error in SQL. Without it the panel was told a coupon had been deleted, and
// answered 204, for one that was never there.
func (s *CouponStore) DeleteCoupon(ctx context.Context, id int) error {
	n, err := s.q.DeleteCoupon(ctx, int64(id))
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *CouponStore) RedeemCoupon(ctx context.Context, id int) (int64, error) {
	return s.q.RedeemCoupon(ctx, int64(id))
}

func (s *CouponStore) GiveBackCouponUse(ctx context.Context, id int) error {
	return s.q.GiveBackCouponUse(ctx, int64(id))
}

func (s *CouponStore) CountCoupons(ctx context.Context) (int64, error) {
	return s.q.CountCoupons(ctx)
}
