package service

import (
	"context"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

type PaymentMethodService struct {
	paymentMethods store.PaymentMethod
}

func NewPaymentMethodService(paymentMethods store.PaymentMethod) *PaymentMethodService {
	return &PaymentMethodService{paymentMethods: paymentMethods}
}

func (s *PaymentMethodService) ListByUser(ctx context.Context, userID int) ([]*model.PaymentMethod, error) {
	return s.paymentMethods.GetPaymentMethodsByUserID(ctx, userID)
}

func (s *PaymentMethodService) Get(ctx context.Context, id int) (*model.PaymentMethod, error) {
	return s.paymentMethods.GetPaymentMethodByID(ctx, id)
}

func (s *PaymentMethodService) Create(ctx context.Context, pm *model.PaymentMethod) (*model.PaymentMethod, error) {
	return s.paymentMethods.CreatePaymentMethod(ctx, pm)
}

func (s *PaymentMethodService) Update(ctx context.Context, id int, pm *model.PaymentMethod) (*model.PaymentMethod, error) {
	return s.paymentMethods.UpdatePaymentMethod(ctx, id, pm)
}

func (s *PaymentMethodService) Delete(ctx context.Context, id int) error {
	return s.paymentMethods.DeletePaymentMethod(ctx, id)
}
