package store

import (
	"errors"

	"github.com/go-sql-driver/mysql"

	"dvbs/internal/dataservice"
)

// TiDB/MySQL error numbers, named once so both transports agree on them.
const (
	// codeDuplicateEntry is a unique index rejection.
	codeDuplicateEntry = 1062
	// codeParentHasChildren is a parent row that still has children. In this
	// schema that is a book that appears in an order: order_items points at it
	// with ON DELETE RESTRICT, so the book cannot go.
	codeParentHasChildren = 1451
	// codeChildHasNoParent is the other half of the same constraint: a child row
	// naming a parent that is not there.
	codeChildHasNoParent = codeParentHasChildren + 1
)

// IsDuplicate reports whether err is a unique index rejection.
//
// It has to recognise two transports, because the same query can fail either
// way. The SQL stores get a *mysql.MySQLError straight from the driver; the
// Data Service stores get a *dataservice.Error, because the statement ran on
// the far side of an HTTP call and the number arrived inside the response body
// rather than attached to the request. Two entry points that meant the same
// thing would otherwise mean two different HTTP statuses to the browser, and
// the panel has one code path for "this ISBN already exists".
func IsDuplicate(err error) bool {
	return isMySQLCode(err, codeDuplicateEntry)
}

// IsForeignKey reports a rejected reference, in either direction.
func IsForeignKey(err error) bool {
	return isMySQLCode(err, codeParentHasChildren) || isMySQLCode(err, codeChildHasNoParent)
}

func isMySQLCode(err error, code uint16) bool {
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number == code
	}
	var apiErr *dataservice.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == int(code)
	}
	return false
}
