package service

import (
	"dvbs/internal/store"
)

// isUniqueViolation reports whether an error is a duplicate-key rejection.
//
// It exists because the pre-flight "does this review already exist" check in
// the service is not the thing that enforces the rule. The unique index is. The
// service check only makes the common case return a friendly message; when two
// requests arrive together, both pass the check and the second one is stopped
// here, by the database, at the moment it tries to write.
//
// Recognising the error rather than string-matching it means the message a
// customer sees does not depend on the wording of a driver error, and it keeps
// working when the query goes through Data Service instead of a direct
// connection: store.IsDuplicate knows about both. The classification itself
// lives there because the transport layer has to answer the same question to
// turn it into a status code.
func isUniqueViolation(err error) bool {
	return err != nil && store.IsDuplicate(err)
}
