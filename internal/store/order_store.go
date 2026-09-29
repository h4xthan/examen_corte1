package store

import (
	"context"
	"database/sql"
	"fmt"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type Order interface {
	GetAllOrders(ctx context.Context) ([]*model.Order, error)
	GetOrderByID(ctx context.Context, id int) (*model.Order, error)
	CreateOrder(ctx context.Context, order *model.Order) (*model.Order, error)
	UpdateOrder(ctx context.Context, id int, order *model.Order) (*model.Order, error)
	DeleteOrder(ctx context.Context, id int) error
	GetOrdersByUserID(ctx context.Context, userID int) ([]*model.Order, error)
	CountOrders(ctx context.Context) (int64, error)
	SumOrderTotals(ctx context.Context) (int64, error)
}

type OrderStore struct {
	q db.Querier
}

func NewOrderStore(q db.Querier) Order {
	return &OrderStore{q: q}
}

func orderFromDB(r db.Order) *model.Order {
	return &model.Order{
		ID:            int64(r.ID),
		UserID:        r.UserID,
		Status:        r.Status,
		SubtotalCents: r.SubtotalCents,
		DiscountCents: r.DiscountCents,
		TotalCents:    r.TotalCents,
		CouponID:      sql.NullInt64{Int64: r.CouponID.Int64, Valid: r.CouponID.Valid},
		CreatedAt:     r.CreatedAt,
	}
}

func (s *OrderStore) GetAllOrders(ctx context.Context) ([]*model.Order, error) {
	rows, err := s.q.GetAllOrders(ctx)
	if err != nil {
		return nil, err
	}
	orders := make([]*model.Order, 0, len(rows))
	for _, r := range rows {
		orders = append(orders, orderFromDB(r))
	}
	return orders, nil
}

func (s *OrderStore) GetOrderByID(ctx context.Context, id int) (*model.Order, error) {
	r, err := s.q.GetOrderByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return orderFromDB(r), nil
}

func (s *OrderStore) CreateOrder(ctx context.Context, order *model.Order) (*model.Order, error) {
	res, err := s.q.CreateOrder(ctx, db.CreateOrderParams{
		UserID:        order.UserID,
		Status:        order.Status,
		SubtotalCents: order.SubtotalCents,
		DiscountCents: order.DiscountCents,
		TotalCents:    order.TotalCents,
		CouponID:      order.CouponID,
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetOrderByID(ctx, int(id))
}

func (s *OrderStore) UpdateOrder(ctx context.Context, id int, order *model.Order) (*model.Order, error) {
	if err := s.q.UpdateOrder(ctx, db.UpdateOrderParams{
		ID:            int64(id),
		Status:        order.Status,
		SubtotalCents: order.SubtotalCents,
		DiscountCents: order.DiscountCents,
		TotalCents:    order.TotalCents,
		CouponID:      sql.NullInt64{Int64: order.CouponID.Int64, Valid: order.CouponID.Valid},
	}); err != nil {
		return nil, err
	}
	return s.GetOrderByID(ctx, id)
}

func (s *OrderStore) DeleteOrder(ctx context.Context, id int) error {
	return s.q.DeleteOrder(ctx, int64(id))
}

func (s *OrderStore) GetOrdersByUserID(ctx context.Context, userID int) ([]*model.Order, error) {
	rows, err := s.q.GetOrdersByUserID(ctx, int64(userID))
	if err != nil {
		return nil, err
	}
	orders := make([]*model.Order, 0, len(rows))
	for _, r := range rows {
		orders = append(orders, orderFromDB(r))
	}
	return orders, nil
}

func (s *OrderStore) CountOrders(ctx context.Context) (int64, error) {
	count, err := s.q.CountOrders(ctx)
	return count, err
}

// SumOrderTotals is revenue, in cents.
//
// The query casts its SUM to SIGNED so that TiDB answers with a BIGINT, and
// sqlc still types the expression as interface{} because a computed column has
// no declared type for it to read. That leaves one assertion here, and it is
// deliberately the narrow one.
//
// The previous version switched over int64, int and float64. float64 is how
// this number got into a 500 in the first place: MySQL widens SUM() over an
// integer column to DECIMAL, the driver hands that back as []byte, none of the
// three cases matched, and GET /admin/stats answered 500 for the whole shop. A
// revenue figure that cannot be represented exactly as an integer is not a
// revenue figure, so a float is refused rather than truncated, and the error
// names the fix instead of the symptom.
func (s *OrderStore) SumOrderTotals(ctx context.Context) (int64, error) {
	sum, err := s.q.SumOrderTotals(ctx)
	if err != nil {
		return 0, err
	}
	if v, ok := sum.(int64); ok {
		return v, nil
	}
	return 0, fmt.Errorf("sum of order totals came back as %T, not int64: "+
		"the query must keep its CAST(... AS SIGNED) or sqlc must be told the type", sum)
}
