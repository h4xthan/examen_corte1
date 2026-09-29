package store

import (
	"context"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type OrderItem interface {
	GetAllOrderItems(ctx context.Context) ([]*model.OrderItem, error)
	GetOrderItemByID(ctx context.Context, id int) (*model.OrderItem, error)
	GetOrderItemsByOrderID(ctx context.Context, orderID int) ([]*model.OrderItem, error)
	CreateOrderItem(ctx context.Context, item *model.OrderItem) (*model.OrderItem, error)
	UpdateOrderItem(ctx context.Context, id int, item *model.OrderItem) (*model.OrderItem, error)
	DeleteOrderItem(ctx context.Context, id int) error
	GetOrderItemOwnerID(ctx context.Context, id int) (int64, error)
}

type OrderItemStore struct {
	q db.Querier
}

func NewOrderItemStore(q db.Querier) OrderItem {
	return &OrderItemStore{q: q}
}

func orderItemFromDB(r db.OrderItem) *model.OrderItem {
	return &model.OrderItem{
		ID:             int64(r.ID),
		OrderID:        r.OrderID,
		BookID:         r.BookID,
		Quantity:       int(r.Quantity),
		UnitPriceCents: r.UnitPriceCents,
		CreatedAt:      r.CreatedAt,
	}
}

func (s *OrderItemStore) GetAllOrderItems(ctx context.Context) ([]*model.OrderItem, error) {
	rows, err := s.q.GetAllOrderItems(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*model.OrderItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, orderItemFromDB(r))
	}
	return items, nil
}

func (s *OrderItemStore) GetOrderItemByID(ctx context.Context, id int) (*model.OrderItem, error) {
	r, err := s.q.GetOrderItemByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return orderItemFromDB(r), nil
}

func (s *OrderItemStore) GetOrderItemsByOrderID(ctx context.Context, orderID int) ([]*model.OrderItem, error) {
	rows, err := s.q.GetOrderItemsByOrderID(ctx, int64(orderID))
	if err != nil {
		return nil, err
	}
	items := make([]*model.OrderItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, orderItemFromDB(r))
	}
	return items, nil
}

func (s *OrderItemStore) CreateOrderItem(ctx context.Context, item *model.OrderItem) (*model.OrderItem, error) {
	res, err := s.q.CreateOrderItem(ctx, db.CreateOrderItemParams{
		OrderID:        item.OrderID,
		BookID:         item.BookID,
		Quantity:       int32(item.Quantity),
		UnitPriceCents: item.UnitPriceCents,
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetOrderItemByID(ctx, int(id))
}

func (s *OrderItemStore) UpdateOrderItem(ctx context.Context, id int, item *model.OrderItem) (*model.OrderItem, error) {
	if err := s.q.UpdateOrderItem(ctx, db.UpdateOrderItemParams{
		ID:             int64(id),
		OrderID:        item.OrderID,
		BookID:         item.BookID,
		Quantity:       int32(item.Quantity),
		UnitPriceCents: item.UnitPriceCents,
	}); err != nil {
		return nil, err
	}
	return s.GetOrderItemByID(ctx, id)
}

func (s *OrderItemStore) DeleteOrderItem(ctx context.Context, id int) error {
	return s.q.DeleteOrderItem(ctx, int64(id))
}

func (s *OrderItemStore) GetOrderItemOwnerID(ctx context.Context, id int) (int64, error) {
	return s.q.GetOrderItemOwnerID(ctx, int64(id))
}
