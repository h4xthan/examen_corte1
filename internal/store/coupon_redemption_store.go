package store

import (
	"context"
	"database/sql"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type CouponRedemption interface {
	CreateCouponRedemption(ctx context.Context, redemption *model.CouponRedemption) (*model.CouponRedemption, error)
	GetCouponRedemption(ctx context.Context, couponID int, userID int) (*model.CouponRedemption, error)
	DeleteCouponRedemptionByOrder(ctx context.Context, orderID int) error
	CountCouponRedemptions(ctx context.Context, couponID int) (int64, error)
}

type CouponRedemptionStore struct {
	q db.Querier
}

func NewCouponRedemptionStore(q db.Querier) CouponRedemption {
	return &CouponRedemptionStore{q: q}
}

func couponRedemptionFromDB(r db.CouponRedemption) *model.CouponRedemption {
	return &model.CouponRedemption{
		ID:         int64(r.ID),
		CouponID:   int64(r.CouponID),
		UserID:     int64(r.UserID),
		OrderID:    sql.NullInt64{Int64: r.OrderID.Int64, Valid: r.OrderID.Valid},
		RedeemedAt: r.RedeemedAt,
	}
}

func (s *CouponRedemptionStore) CreateCouponRedemption(ctx context.Context, redemption *model.CouponRedemption) (*model.CouponRedemption, error) {
	_, err := s.q.CreateCouponRedemption(ctx, db.CreateCouponRedemptionParams{
		CouponID: redemption.CouponID,
		UserID:   redemption.UserID,
		OrderID:  redemption.OrderID,
	})
	if err != nil {
		return nil, err
	}
	return s.GetCouponRedemption(ctx, int(redemption.CouponID), int(redemption.UserID))
}

func (s *CouponRedemptionStore) GetCouponRedemption(ctx context.Context, couponID int, userID int) (*model.CouponRedemption, error) {
	r, err := s.q.GetCouponRedemption(ctx, db.GetCouponRedemptionParams{
		CouponID: int64(couponID),
		UserID:   int64(userID),
	})
	if err != nil {
		return nil, err
	}
	return couponRedemptionFromDB(r), nil
}

func (s *CouponRedemptionStore) DeleteCouponRedemptionByOrder(ctx context.Context, orderID int) error {
	return s.q.DeleteCouponRedemptionByOrder(ctx, sql.NullInt64{Int64: int64(orderID), Valid: true})
}

func (s *CouponRedemptionStore) CountCouponRedemptions(ctx context.Context, couponID int) (int64, error) {
	return s.q.CountCouponRedemptions(ctx, int64(couponID))
}
