package middleware

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		// Mirror the stdlib: ignore repeat calls rather than emitting a
		// "superfluous WriteHeader" warning and corrupting the body.
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

// WrittenHeader reports the committed status, or 0 when nothing has been
// written yet. The 0 is load-bearing: Recovery uses it to decide whether it may
// still emit a 500, and returning the initial 200 would make a panic silently
// look like a successful empty response.
func (r *statusRecorder) WrittenHeader() int {
	if !r.wroteHeader {
		return 0
	}
	return r.status
}

// The remaining methods keep the wrapper transparent: a plain embedding would
// otherwise hide these capabilities from handlers that type-assert for them and
// silently downgrade streaming, hijacking and efficient copies to buffering.

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		if !r.wroteHeader {
			r.WriteHeader(http.StatusOK)
		}
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (r *statusRecorder) Push(target string, opts *http.PushOptions) error {
	if p, ok := r.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		if !r.wroteHeader {
			r.WriteHeader(http.StatusOK)
		}
		return rf.ReadFrom(src)
	}
	// Fall back to a plain copy through Write, which still records the status.
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return io.Copy(struct{ io.Writer }{r.ResponseWriter}, src)
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
