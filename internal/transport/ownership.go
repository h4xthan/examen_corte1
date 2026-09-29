package transport

import (
	"net/http"

	"dvbs/internal/middleware"
	"dvbs/internal/model"
)

// authorizeOwner is the single check that every route taking an {id} from the
// path has to pass.
//
// The bug it exists to prevent is the reason this application was a target: a
// handler that trusts the id in the URL. /users/17 is a valid request for user
// 17 and for everybody else, and nothing in the plumbing says which. Twenty-four
// routes had no such check, so any authenticated token could read and modify
// every other account.
//
// Two decisions are deliberate:
//
// A mismatch answers 404, never 403. "Forbidden" confirms the resource exists,
// which is exactly the oracle needed to enumerate other people's ids. A caller
// must not be able to tell "no such row" from "not yours".
//
// An admin passes. Moderating reviews, managing orders and administering accounts
// are the job, and the role comes from the database on every request, so a
// demoted admin loses access immediately rather than when a token expires.
func authorizeOwner(w http.ResponseWriter, r *http.Request, ownerID int64) bool {
	if middleware.RoleFrom(r.Context()) == model.RoleAdmin {
		return true
	}

	sessionUser, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		// Auth should have rejected this already. Answering 404 rather than 401
		// keeps the failure mode identical to every other rejected caller.
		writeError(w, http.StatusNotFound, errNotFound)
		return false
	}

	if sessionUser != ownerID {
		writeError(w, http.StatusNotFound, errNotFound)
		return false
	}
	return true
}
