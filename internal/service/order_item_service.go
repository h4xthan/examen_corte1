package service

import (
	"context"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

type OrderItemService struct {
	// runner is how these three operations become atomic. Each of them moves
	// stock and writes a row, and doing the two in separate statements means a
	// failure between them loses inventory that nothing will ever return.
	runner     *store.Runner
	orderItems store.OrderItem
	books      store.Store
	orders     store.Order
}

func NewOrderItemService(runner *store.Runner, orderItems store.OrderItem, books store.Store, orders store.Order) *OrderItemService {
	return &OrderItemService{runner: runner, orderItems: orderItems, books: books, orders: orders}
}

func (s *OrderItemService) ListByOrder(ctx context.Context, orderID int) ([]*model.OrderItem, error) {
	return s.orderItems.GetOrderItemsByOrderID(ctx, orderID)
}

func (s *OrderItemService) GetItem(ctx context.Context, id int) (*model.OrderItem, error) {
	return s.orderItems.GetOrderItemByID(ctx, id)
}

// OwnerID reports which account an order item ultimately belongs to, following
// the item to its order. The handler uses it to answer the ownership question
// without loading rows it would then have to trust.
func (s *OrderItemService) OwnerID(ctx context.Context, id int) (int64, error) {
	return s.orderItems.GetOrderItemOwnerID(ctx, id)
}

// AddItem appends a line to an order.
//
// unit_price is not a parameter. The previous version took the whole
// model.OrderItem from the body, which meant the client chose the price it
// would be charged and chose the book it was charged for, and quantity was an
// unchecked integer. This one takes the two things a client is allowed to
// decide, which book and how many, and looks up the rest.
//
// The price read, the stock reservation and the row insert are one transaction.
// Reserving first and inserting second without one was a silent leak: a failure
// on the insert — a foreign key, a serialisation conflict, a dropped connection
// — left the copy counted out of stock with no line explaining why, and the
// only thing that ever returns stock is a line being edited or deleted.
func (s *OrderItemService) AddItem(ctx context.Context, orderID int, bookID int64, quantity int) (*model.OrderItem, error) {
	if quantity < 1 || quantity > maxLineQuantity {
		return nil, ErrInvalidQuantity
	}

	var created *model.OrderItem
	err := s.runner.WithinTx(ctx, func(q store.Queriers) error {
		// The price is whatever the catalogue says it is right now, not whatever
		// the request claimed, and it is read inside the transaction so a
		// concurrent price change is either seen or the whole line fails.
		book, err := q.Books.GetBookByID(ctx, int(bookID))
		if err != nil {
			return err
		}

		// The conditional update is the reservation. It cannot go below zero even
		// if two requests for the last copy arrive together, and the row count is
		// how this code learns it lost the race.
		affected, err := q.Books.DecrementBookStock(ctx, int(bookID), quantity)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrOutOfStock
		}

		created, err = q.OrderItems.CreateOrderItem(ctx, &model.OrderItem{
			OrderID:        int64(orderID),
			BookID:         bookID,
			Quantity:       quantity,
			UnitPriceCents: book.PriceCents,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateItem changes the quantity of a line.
//
// The price is recomputed from the catalogue, and order_id and book_id are taken
// from the stored row, so editing a line can neither move it to another order
// nor reprice it.
func (s *OrderItemService) UpdateItem(ctx context.Context, id int, quantity int) (*model.OrderItem, error) {
	if quantity < 1 || quantity > maxLineQuantity {
		return nil, ErrInvalidQuantity
	}

	var updated *model.OrderItem
	err := s.runner.WithinTx(ctx, func(q store.Queriers) error {
		existing, err := q.OrderItems.GetOrderItemByID(ctx, id)
		if err != nil {
			return err
		}

		book, err := q.Books.GetBookByID(ctx, int(int(existing.BookID)))
		if err != nil {
			return err
		}

		// Return the difference before taking the new one, so raising the
		// quantity cannot oversell and lowering it does not manufacture stock.
		//
		// The original quantity is read in the same transaction that changes
		// it, so two concurrent edits cannot both compute their delta against
		// the same starting number and move stock by the wrong amount.
		if delta := quantity - existing.Quantity; delta != 0 {
			if delta > 0 {
				affected, err := q.Books.DecrementBookStock(ctx, int(int(existing.BookID)), delta)
				if err != nil {
					return err
				}
				if affected == 0 {
					return ErrOutOfStock
				}
			} else {
				if err := q.Books.IncrementBookStock(ctx, int(existing.BookID), -delta); err != nil {
					return err
				}
			}
		}

		existing.Quantity = quantity
		existing.UnitPriceCents = book.PriceCents
		updated, err = q.OrderItems.UpdateOrderItem(ctx, id, existing)
		return err
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteItem removes a line and puts its stock back, which would otherwise be
// lost: the reservation happened at add time and nothing else returns it.
//
// The delete and the restock are one transaction, and the quantity that goes
// back is the one read in that same transaction. Doing it the other way round —
// delete, then read what the line was to find out how much to give back — races:
// a second delete would read nothing and hand the stock back twice.
func (s *OrderItemService) DeleteItem(ctx context.Context, id int) error {
	return s.runner.WithinTx(ctx, func(q store.Queriers) error {
		existing, err := q.OrderItems.GetOrderItemByID(ctx, id)
		if err != nil {
			return err
		}
		if err := q.OrderItems.DeleteOrderItem(ctx, id); err != nil {
			return err
		}
		return q.Books.IncrementBookStock(ctx, int(int(existing.BookID)), existing.Quantity)
	})
}
