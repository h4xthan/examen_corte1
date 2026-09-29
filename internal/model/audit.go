package model

import "time"

// AuditLog is one row of operating history: who did what, to which row, and
// a short human line about it.
//
// Every id in this struct crosses the wire quoted, like every id in the app:
// AUTO_RANDOM hands out 19-digit values that a browser JSON.parse would round.
// user_id and entity_id are nullable — a public catalogue read has no actor,
// and a backup has no row it touched — so they are pointers that marshal to
// JSON null rather than to an invented "0".
type AuditLog struct {
	ID        int64     `json:"id,string"`
	UserID    *int64    `json:"user_id,string"`
	UserEmail *string   `json:"user_email"`
	Entity    string    `json:"entity"`
	Action    string    `json:"action"`
	EntityID  *int64    `json:"entity_id,string"`
	Details   string    `json:"details"`
	CreatedAt time.Time `json:"created_at"`
}

// Entities the panels record and the history tab filters on.
const (
	AuditEntityBook   = "book"
	AuditEntityUser   = "user"
	AuditEntityBackup = "backup"
)

// Actions recorded for those entities.
const (
	AuditActionCreate = "create"
	AuditActionUpdate = "update"
	AuditActionDelete = "delete"
	AuditActionRead   = "read"
	AuditActionRole   = "role"
	AuditActionActive = "active"
)
