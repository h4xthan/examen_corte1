package service

import (
	"context"

	"errors"
	"fmt"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

// ErrOrderStatusInvalid is returned for a status outside the closed set. The
// CHECK constraint in 000006 rejects the same values, so this is the courtesy
// half: it answers with a message instead of letting the database produce a 500
// that quotes a constraint name.
var ErrOrderStatusInvalid = errors.New("invalid order status")

// orderStatuses is the vocabulary of the column. It matches the CHECK constraint
// in 000006 exactly, and 'completed' is load-bearing twice over: checkout creates
// it, and the review rule accepts a purchase only from an order in that state.
var orderStatuses = map[string]struct{}{
	"pending":   {},
	"completed": {},
	"shipped":   {},
	"delivered": {},
	"cancelled": {},
	"refunded":  {},
}

func validateOrderStatus(status string) error {
	if _, ok := orderStatuses[status]; !ok {
		return fmt.Errorf("%w: %q is not one of pending, completed, shipped, delivered, cancelled, refunded",
			ErrOrderStatusInvalid, status)
	}
	return nil
}

// SetStatus is the administrative transition, and the only way a status changes
// after checkout.
//
// UpdateOrder, which writes the whole row, is the right tool for nothing a
// request should be doing; it is kept because the admin panel and the seed both
// use it, and it is only ever called with a row that was read first.
func (s *OrderService) SetStatus(ctx context.Context, id int, status string) (*model.Order, error) {
	if err := validateOrderStatus(status); err != nil {
		return nil, err
	}
	order, err := s.orders.GetOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	order.Status = status
	return s.orders.UpdateOrder(ctx, id, order)
}

type OrderWithItems struct {
	model.Order
	Items []*model.OrderItem `json:"items"`
}

type OrderService struct {
	orders     store.Order
	orderItems store.OrderItem
}

func NewOrderService(orders store.Order, orderItems store.OrderItem) *OrderService {
	return &OrderService{orders: orders, orderItems: orderItems}
}

func (s *OrderService) ListOrders(ctx context.Context) ([]*model.Order, error) {
	return s.orders.GetAllOrders(ctx)
}

// ListForUser is the customer-facing listing. It is a separate method rather
// than a filter applied by the handler so that the scoping lives next to the
// query and cannot be forgotten at a call site.
func (s *OrderService) ListForUser(ctx context.Context, userID int64) ([]*model.Order, error) {
	return s.orders.GetOrdersByUserID(ctx, int(userID))
}

// GetOrder returns the order alone, which is what the ownership check needs
// before the caller is allowed to see its items.
func (s *OrderService) GetOrder(ctx context.Context, id int) (*model.Order, error) {
	return s.orders.GetOrderByID(ctx, id)
}

// ListItems returns the lines of an order. Callers must have already
// authorised against the order.
func (s *OrderService) ListItems(ctx context.Context, orderID int) ([]*model.OrderItem, error) {
	return s.orderItems.GetOrderItemsByOrderID(ctx, orderID)
}

func (s *OrderService) GetOrderWithItems(ctx context.Context, id int) (*OrderWithItems, error) {
	order, err := s.orders.GetOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := s.orderItems.GetOrderItemsByOrderID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &OrderWithItems{Order: *order, Items: items}, nil
}

func (s *OrderService) CreateOrder(ctx context.Context, order *model.Order) (*model.Order, error) {
	return s.orders.CreateOrder(ctx, order)
}

func (s *OrderService) UpdateOrder(ctx context.Context, id int, order *model.Order) (*model.Order, error) {
	return s.orders.UpdateOrder(ctx, id, order)
}

func (s *OrderService) DeleteOrder(ctx context.Context, id int) error {
	return s.orders.DeleteOrder(ctx, id)
}
