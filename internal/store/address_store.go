package store

import (
	"context"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type Address interface {
	GetAllAddresses(ctx context.Context) ([]*model.Address, error)
	GetAddressByID(ctx context.Context, id int) (*model.Address, error)
	GetAddressesByUserID(ctx context.Context, user_id int) ([]*model.Address, error)
	CreateAddress(ctx context.Context, address *model.Address) (*model.Address, error)
	UpdateAddress(ctx context.Context, id int, address *model.Address) (*model.Address, error)
	DeleteAddress(ctx context.Context, id int) error
}

type AddressStore struct {
	q db.Querier
}

func NewAddressStore(q db.Querier) Address {
	return &AddressStore{q: q}
}

func addressFromDB(r db.Address) *model.Address {
	return &model.Address{
		ID:     int64(r.ID),
		UserID: r.UserID,
		Street: r.Street,
		City:   r.City,
		Zip:    r.Zip,
	}
}

func (s *AddressStore) GetAllAddresses(ctx context.Context) ([]*model.Address, error) {
	rows, err := s.q.GetAllAddresses(ctx)
	if err != nil {
		return nil, err
	}
	addresses := make([]*model.Address, 0, len(rows))
	for _, r := range rows {
		addresses = append(addresses, addressFromDB(r))
	}
	return addresses, nil
}

func (s *AddressStore) GetAddressByID(ctx context.Context, id int) (*model.Address, error) {
	r, err := s.q.GetAddressByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return addressFromDB(r), nil
}

func (s *AddressStore) GetAddressesByUserID(ctx context.Context, user_id int) ([]*model.Address, error) {
	rows, err := s.q.GetAddressesByUserID(ctx, int64(user_id))
	if err != nil {
		return nil, err
	}
	addresses := make([]*model.Address, 0, len(rows))
	for _, r := range rows {
		addresses = append(addresses, addressFromDB(r))
	}
	return addresses, nil
}

func (s *AddressStore) CreateAddress(ctx context.Context, address *model.Address) (*model.Address, error) {
	res, err := s.q.CreateAddress(ctx, db.CreateAddressParams{
		UserID: address.UserID,
		Street: address.Street,
		City:   address.City,
		Zip:    address.Zip,
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetAddressByID(ctx, int(id))
}

func (s *AddressStore) UpdateAddress(ctx context.Context, id int, address *model.Address) (*model.Address, error) {
	if err := s.q.UpdateAddress(ctx, db.UpdateAddressParams{
		ID:     int64(id),
		UserID: address.UserID,
		Street: address.Street,
		City:   address.City,
		Zip:    address.Zip,
	}); err != nil {
		return nil, err
	}
	return s.GetAddressByID(ctx, id)
}

func (s *AddressStore) DeleteAddress(ctx context.Context, id int) error {
	return s.q.DeleteAddress(ctx, int64(id))
}
