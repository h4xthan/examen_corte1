package service

import (
	"context"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

type AddressService struct {
	addresses store.Address
}

func NewAddressService(addresses store.Address) *AddressService {
	return &AddressService{addresses: addresses}
}

func (s *AddressService) ListByUser(ctx context.Context, userID int) ([]*model.Address, error) {
	return s.addresses.GetAddressesByUserID(ctx, userID)
}

func (s *AddressService) ListAll(ctx context.Context) ([]*model.Address, error) {
	return s.addresses.GetAllAddresses(ctx)
}

func (s *AddressService) Get(ctx context.Context, id int) (*model.Address, error) {
	return s.addresses.GetAddressByID(ctx, id)
}

func (s *AddressService) Create(ctx context.Context, address *model.Address) (*model.Address, error) {
	return s.addresses.CreateAddress(ctx, address)
}

func (s *AddressService) Update(ctx context.Context, id int, address *model.Address) (*model.Address, error) {
	return s.addresses.UpdateAddress(ctx, id, address)
}

func (s *AddressService) Delete(ctx context.Context, id int) error {
	return s.addresses.DeleteAddress(ctx, id)
}
