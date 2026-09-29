package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Every store method used to open its own context.Background(). The signature
// said nothing about cancellation, so a client that disconnected, or a request
// that blew its deadline, left the query running to completion against the
// database: the work was paid for, the rows were scanned, and the result was
// thrown away.
//
// The stores now take the request's context. This is the test that says so, and
// it is a store test rather than a handler test on purpose: the thing being
// asserted is that the value reaches pgx, and a handler in between could
// swallow the error and still return a plausible-looking response.
func TestStoreHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	// A real row, because the baseline below asserts these calls succeed and a
	// missing book would make "read one" fail for a reason that has nothing to
	// do with cancellation.
	books, err := NewBookStore(testQueries).GetAllBooks(t.Context())
	if err != nil {
		t.Fatalf("GetAllBooks: %v", err)
	}
	if len(books) == 0 {
		t.Skip("no books in the database; nothing to read")
	}
	existingID := int(books[0].ID)

	cases := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{
			name: "read one",
			call: func(ctx context.Context) error {
				_, err := NewBookStore(testQueries).GetBookByID(ctx, existingID)
				return err
			},
		},
		{
			name: "read many",
			call: func(ctx context.Context) error {
				_, err := NewBookStore(testQueries).GetAllBooks(ctx)
				return err
			},
		},
		{
			name: "count",
			call: func(ctx context.Context) error {
				_, err := NewBookStore(testQueries).CountBooks(ctx)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The same call with a live context has to work, otherwise this
			// test would pass for the wrong reason: a query that always errors
			// is not a query that respects cancellation.
			if err := tc.call(t.Context()); err != nil {
				t.Fatalf("baseline with a live context: %v", err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := tc.call(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("with a cancelled context: err = %v, want context.Canceled", err)
			}
		})
	}
}

// A deadline that has already passed is the same failure as a cancellation, and
// it is the one that actually happens in production: the write timeout, the
// proxy timeout, the client's patience. It gets its own case so that fixing one
// of the two does not silently leave the other broken.
func TestStoreHonoursAnExpiredDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	if _, err := NewBookStore(testQueries).GetAllBooks(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("with an expired deadline: err = %v, want context.DeadlineExceeded", err)
	}
}
