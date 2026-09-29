package transport

import (
	"errors"
	"log/slog"
	"net/http"
)

// errRequestTooLarge is returned by decodeJSON when the body exceeds
// maxBodyBytes. It is a sentinel so every handler can keep calling
// writeError(w, http.StatusBadRequest, err) and still get a 413 back without
// having to know about body limits.
var errRequestTooLarge = errors.New("request body too large")

// errInvalidJSON marks a body that failed to decode for any reason other than
// size. The detail is logged, never returned, because decoder errors quote the
// offending input.
var errInvalidJSON = errors.New("invalid JSON body")

// errNotFound is the response body for a missing row. It is deliberately vague:
// telling an attacker "this exists but is not yours" versus "this does not
// exist" is exactly the oracle an IDOR hunt needs, so both cases answer the
// same way.
var errNotFound = errors.New("not found")

// errUnauthenticated is what a handler answers when it needs a session and the
// middleware chain did not already guarantee one. Auth rejects these requests
// first in practice, so this is the backstop that keeps a future routing mistake
// from turning into an unauthenticated write.
var errUnauthenticated = errors.New("authentication required")

// errInvalidPathID is returned when the {id} in the path is not an integer. It
// is a sentinel rather than a fresh errors.New at each call site so the message
// is identical everywhere and tests can match on it.
var errInvalidPathID = errors.New("invalid id in path")

// writeError sends err to the client.
//
// Two rules, and the split between them matters:
//
//   - 4xx errors are authored by us (validation, business rules) and their text
//     is part of the API contract, so it goes through verbatim.
//   - 5xx errors are failures we did not anticipate, often straight from the
//     database. The text goes to the log and the client gets a generic message,
//     so a schema change or a constraint name never leaks into a response.
func writeError(w http.ResponseWriter, status int, err error) {
	if errors.Is(err, errRequestTooLarge) {
		status = http.StatusRequestEntityTooLarge
	}

	if status >= http.StatusInternalServerError {
		slog.Error("request failed", "status", status, "err", err)
		writeJSON(w, status, map[string]string{"error": http.StatusText(status)})
		return
	}

	writeJSON(w, status, map[string]string{"error": err.Error()})
}
