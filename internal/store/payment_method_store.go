package store

import (
	"context"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type PaymentMethod interface {
	GetPaymentMethodByID(ctx context.Context, id int) (*model.PaymentMethod, error)
	GetPaymentMethodsByUserID(ctx context.Context, user_id int) ([]*model.PaymentMethod, error)
	CreatePaymentMethod(ctx context.Context, payment_method *model.PaymentMethod) (*model.PaymentMethod, error)
	UpdatePaymentMethod(ctx context.Context, id int, payment_method *model.PaymentMethod) (*model.PaymentMethod, error)
	DeletePaymentMethod(ctx context.Context, id int) error
	CountPaymentMethods(ctx context.Context) (int64, error)
}

type PaymentMethodStore struct {
	q db.Querier
}

func NewPaymentMethodStore(q db.Querier) PaymentMethod {
	return &PaymentMethodStore{q: q}
}

func paymentMethodFromDB(r db.PaymentMethod) *model.PaymentMethod {
	return &model.PaymentMethod{
		ID:          int64(r.ID),
		UserID:      r.UserID,
		Brand:       r.Brand,
		Last4:       r.Last4,
		ExpiryMonth: int(r.ExpiryMonth),
		ExpiryYear:  int(r.ExpiryYear),
		CreatedAt:   r.CreatedAt,
	}
}

func (s *PaymentMethodStore) GetPaymentMethodByID(ctx context.Context, id int) (*model.PaymentMethod, error) {
	r, err := s.q.GetPaymentMethodByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return paymentMethodFromDB(r), nil
}

func (s *PaymentMethodStore) GetPaymentMethodsByUserID(ctx context.Context, user_id int) ([]*model.PaymentMethod, error) {
	rows, err := s.q.GetPaymentMethodsByUserID(ctx, int64(user_id))
	if err != nil {
		return nil, err
	}
	methods := make([]*model.PaymentMethod, 0, len(rows))
	for _, r := range rows {
		methods = append(methods, paymentMethodFromDB(r))
	}
	return methods, nil
}

func (s *PaymentMethodStore) CreatePaymentMethod(ctx context.Context, payment_method *model.PaymentMethod) (*model.PaymentMethod, error) {
	res, err := s.q.CreatePaymentMethod(ctx, db.CreatePaymentMethodParams{
		UserID:      payment_method.UserID,
		Brand:       payment_method.Brand,
		Last4:       payment_method.Last4,
		ExpiryMonth: int16(payment_method.ExpiryMonth),
		ExpiryYear:  int16(payment_method.ExpiryYear),
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetPaymentMethodByID(ctx, int(id))
}

func (s *PaymentMethodStore) UpdatePaymentMethod(ctx context.Context, id int, payment_method *model.PaymentMethod) (*model.PaymentMethod, error) {
	if err := s.q.UpdatePaymentMethod(ctx, db.UpdatePaymentMethodParams{
		ID:          int64(id),
		Brand:       payment_method.Brand,
		Last4:       payment_method.Last4,
		ExpiryMonth: int16(payment_method.ExpiryMonth),
		ExpiryYear:  int16(payment_method.ExpiryYear),
	}); err != nil {
		return nil, err
	}
	return s.GetPaymentMethodByID(ctx, id)
}

func (s *PaymentMethodStore) DeletePaymentMethod(ctx context.Context, id int) error {
	return s.q.DeletePaymentMethod(ctx, int64(id))
}

func (s *PaymentMethodStore) CountPaymentMethods(ctx context.Context) (int64, error) {
	return s.q.CountPaymentMethods(ctx)
}
