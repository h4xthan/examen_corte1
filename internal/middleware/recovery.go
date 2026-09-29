package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery converts a panic in any downstream handler into a clean 500 JSON
// response with a structured server-side log entry.
//
// The stdlib already recovers panics per connection, so this is not about
// keeping the process alive. What it does not do is produce a usable response:
// the client sees a reset connection with no status and no body, and the server
// side gets a raw stack dump on stderr instead of a slog event. For an API whose
// frontend parses JSON, that difference is the whole point.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}

			// http.ErrAbortHandler is the documented way to abandon a
			// response on purpose. It is not a crash, so pass it through.
			if err, ok := rec.(error); ok && err == http.ErrAbortHandler {
				panic(rec)
			}

			slog.Error("panic in handler",
				"method", r.Method,
				"path", r.URL.Path,
				"panic", rec,
				"stack", string(debug.Stack()),
			)

			// A handler may already have written a status line before
			// panicking, in which case there is nothing left to say and
			// writing again would corrupt the response.
			if !headerWritten(w) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// headerWritten reports whether the response has already been committed, using
// the optional interfaces net/http exposes for exactly this check.
func headerWritten(w http.ResponseWriter) bool {
	if f, ok := w.(interface{ WrittenHeader() int }); ok {
		return f.WrittenHeader() != 0
	}
	return false
}
