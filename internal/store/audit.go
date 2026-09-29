package store

import (
	"context"
	"database/sql"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

// Audit is the operating-history store behind the three interfaces' history.
type Audit interface {
	Record(ctx context.Context, e *model.AuditLog) error
	ListRecent(ctx context.Context, entity string, limit int) ([]*model.AuditLog, error)
}

type AuditStore struct {
	q db.Querier
}

func NewAuditStore(q db.Querier) Audit {
	return &AuditStore{q: q}
}

func auditFromDB(r db.AuditLog) *model.AuditLog {
	var userID *int64
	if r.UserID.Valid {
		v := r.UserID.Int64
		userID = &v
	}
	var userEmail *string
	if r.UserEmail.Valid {
		v := r.UserEmail.String
		userEmail = &v
	}
	var entityID *int64
	if r.EntityID.Valid {
		v := r.EntityID.Int64
		entityID = &v
	}
	return &model.AuditLog{
		ID:        r.ID,
		UserID:    userID,
		UserEmail: userEmail,
		Entity:    r.Entity,
		Action:    r.Action,
		EntityID:  entityID,
		Details:   r.Details.String,
		CreatedAt: r.CreatedAt,
	}
}

func (s *AuditStore) Record(ctx context.Context, e *model.AuditLog) error {
	userID := sql.NullInt64{}
	if e.UserID != nil {
		userID.Int64 = *e.UserID
		userID.Valid = true
	}
	userEmail := sql.NullString{}
	if e.UserEmail != nil {
		userEmail.String = *e.UserEmail
		userEmail.Valid = true
	}
	entityID := sql.NullInt64{}
	if e.EntityID != nil {
		entityID.Int64 = *e.EntityID
		entityID.Valid = true
	}
	_, err := s.q.CreateAuditLog(ctx, db.CreateAuditLogParams{
		UserID:    userID,
		UserEmail: userEmail,
		Entity:    e.Entity,
		Action:    e.Action,
		EntityID:  entityID,
		Details:   sql.NullString{String: e.Details, Valid: e.Details != ""},
	})
	return err
}

func (s *AuditStore) ListRecent(ctx context.Context, entity string, limit int) ([]*model.AuditLog, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := s.q.ListAuditLogs(ctx, db.ListAuditLogsParams{
		Entity: entity,
		Limit:  int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*model.AuditLog, 0, len(rows))
	for _, r := range rows {
		out = append(out, auditFromDB(r))
	}
	return out, nil
}
