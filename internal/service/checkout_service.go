package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

var (
	ErrInsufficientCredit = errors.New("insufficient credit")
	ErrCouponExhausted    = errors.New("coupon is expired, used up, or already yours")
	ErrCouponAlreadyUsed  = errors.New("coupon already redeemed by this customer")
	ErrEmptyCart          = errors.New("cart is empty")
	ErrInvalidQuantity    = errors.New("quantity must be between 1 and 10")
	ErrOutOfStock         = errors.New("not enough stock")
)

const (
	maxLineQuantity  = 10
	maxLinesPerOrder = 50
)

type CheckoutItem struct {
	// Quoted, because the client read the id off a response and sent back what
	// it was given. See the note on model.Book.ID.
	BookID   int64 `json:"book_id,string"`
	Quantity int   `json:"quantity"`
}

type CheckoutRequest struct {
	Items      []CheckoutItem `json:"items"`
	CouponCode string         `json:"coupon_code"`
}

type CheckoutResult struct {
	Order        *model.Order       `json:"order"`
	Items        []*model.OrderItem `json:"items"`
	Coupon       *model.Coupon      `json:"coupon,omitempty"`
	BalanceCents int64              `json:"balance_cents"`
}

type CheckoutService struct {
	runner *store.Runner
	users  store.User
}

func NewCheckoutService(runner *store.Runner, users store.User) *CheckoutService {
	return &CheckoutService{runner: runner, users: users}
}

func (s *CheckoutService) Checkout(ctx context.Context, userID int64, req *CheckoutRequest) (*CheckoutResult, error) {
	if len(req.Items) == 0 {
		return nil, ErrEmptyCart
	}
	if len(req.Items) > maxLinesPerOrder {
		return nil, fmt.Errorf("an order may not exceed %d lines", maxLinesPerOrder)
	}

	var result *CheckoutResult

	err := s.runner.WithinTx(ctx, func(q store.Queriers) error {
		// Lock the account row before any other statement. On TiDB the debit's
		// WHERE clause is only guaranteed to see committed data once this
		// transaction holds the lock; without it, two concurrent checkouts can
		// both match balance_cents >= total and both debit, driving the balance
		// negative. Acquiring it here serializes checkout by account.
		if _, err := q.Users.GetUserBalanceForUpdate(ctx, int(userID)); err != nil {
			return fmt.Errorf("lock account balance: %w", err)
		}

		var subtotal int64

		type line struct {
			bookID   int64
			quantity int
			price    int64
		}
		lines := make([]line, 0, len(req.Items))

		for _, item := range req.Items {
			if item.Quantity < 1 || item.Quantity > maxLineQuantity {
				return ErrInvalidQuantity
			}

			book, err := q.Books.GetBookByID(ctx, int(item.BookID))
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrNoRows) {
					return fmt.Errorf("book %d does not exist", item.BookID)
				}
				return err
			}

			affected, err := q.Books.DecrementBookStock(ctx, int(item.BookID), item.Quantity)
			if err != nil {
				return err
			}
			if affected == 0 {
				return fmt.Errorf("%w: %s", ErrOutOfStock, book.Title)
			}

			lines = append(lines, line{bookID: item.BookID, quantity: item.Quantity, price: book.PriceCents})
			subtotal += book.PriceCents * int64(item.Quantity)
		}

		discount := int64(0)
		var applied *model.Coupon
		if req.CouponCode != "" {
			coupon, err := q.Coupons.GetCouponByCode(ctx, req.CouponCode)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrNoRows) {
					return fmt.Errorf("%w: no such code", ErrCouponExhausted)
				}
				return err
			}

			if _, err := q.CouponUses.GetCouponRedemption(ctx, int(coupon.ID), int(userID)); err == nil {
				return fmt.Errorf("%w: %s", ErrCouponAlreadyUsed, coupon.Code)
			}

			affected, err := q.Coupons.RedeemCoupon(ctx, int(coupon.ID))
			if err != nil {
				return err
			}
			if affected == 0 {
				return fmt.Errorf("%w: %s", ErrCouponExhausted, coupon.Code)
			}

			discount = subtotal * int64(coupon.DiscountPercent) / 100
			applied = coupon
		}

		total := subtotal - discount
		if total < 0 {
			total = 0
		}

		debited, err := q.Users.DebitUserBalanceIfSufficient(ctx, int(userID), total)
		if err != nil {
			return err
		}
		if debited == 0 {
			return fmt.Errorf("%w: %d cents required", ErrInsufficientCredit, total)
		}

		var couponID sql.NullInt64
		if applied != nil {
			couponID = sql.NullInt64{Int64: applied.ID, Valid: true}
		}

		order, err := q.Orders.CreateOrder(ctx, &model.Order{
			UserID:        userID,
			Status:        "completed",
			SubtotalCents: subtotal,
			DiscountCents: discount,
			TotalCents:    total,
			CouponID:      couponID,
		})
		if err != nil {
			return err
		}

		stored := make([]*model.OrderItem, 0, len(lines))
		for _, l := range lines {
			created, err := q.OrderItems.CreateOrderItem(ctx, &model.OrderItem{
				OrderID:        order.ID,
				BookID:         l.bookID,
				Quantity:       l.quantity,
				UnitPriceCents: l.price,
			})
			if err != nil {
				return err
			}
			stored = append(stored, created)
		}

		if applied != nil {
			if _, err := q.CouponUses.CreateCouponRedemption(ctx, &model.CouponRedemption{
				CouponID: applied.ID,
				UserID:   userID,
				OrderID:  sql.NullInt64{Int64: order.ID, Valid: true},
			}); err != nil {
				return fmt.Errorf("%w: %s", ErrCouponAlreadyUsed, applied.Code)
			}
		}

		user, err := q.Users.GetUserByID(ctx, int(userID))
		if err != nil {
			return err
		}

		if applied != nil {
			applied.UsedCount++
		}

		result = &CheckoutResult{
			Order:        order,
			Items:        stored,
			Coupon:       applied,
			BalanceCents: user.BalanceCents,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
