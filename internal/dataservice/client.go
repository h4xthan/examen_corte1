// Package dataservice talks to TiDB Cloud Data Service: an HTTPS API in front
// of the cluster where each endpoint is a SQL statement written by hand and
// deployed as a REST route.
//
// It exists for the read-mostly paths of the app, where an HTTP round trip is
// cheaper than reasoning about a connection pool that the free tier closes
// under our feet. It is not a replacement for the SQL driver. A Data Service
// call is its own connection with its own transaction, so it cannot join a
// local one: anything that has to be atomic with a balance debit stays on
// `database/sql`. See the split table in AGENTS.md before moving a method here.
//
// The other reason it is safe to expose through the admin panel is that the key
// never leaves the server. The panel talks to this package's caller, the Go API,
// which already holds the session and re-checks the role; the private key lives
// in the environment and is not in any response body.
package dataservice

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultTimeout is a whole endpoint round trip: the connection to TiDB, the
	// query, and the row limit. An endpoint that has not answered by now is
	// reported as failed rather than left to pile up goroutines.
	defaultTimeout = 10 * time.Second

	// maxResponseBytes caps what a single call will read. The endpoints return
	// rows of a book catalogue, so anything approaching this means the endpoint
	// is not what the caller thinks it is.
	maxResponseBytes = 8 << 20 // 8 MiB

	// TiDB/MySQL error codes worth naming. They are compared by number so the
	// message a user sees does not depend on a driver's wording.
	codeDuplicateEntry = 1062
	// The two halves of a foreign key failure, which are not the same number and
	// are easy to confuse: 1451 is a parent that still has children, which is
	// what deleting a book that appears in an order produces, and 1452 is a child
	// whose parent is missing. Both are a conflict, and to a caller they are the
	// same problem.
	codeParentHasChildren = 1451
	codeChildHasNoParent  = codeParentHasChildren + 1
)

// Config is the connection detail of one Data App. Both keys are required even
// though only the private half is secret: they are the username and password of
// the same credential.
type Config struct {
	BaseURL    string
	PublicKey  string
	PrivateKey string
	Timeout    time.Duration

	// HTTPClient replaces the default transport. Its Timeout is ignored and
	// replaced by the one above, so a caller cannot accidentally opt out of the
	// deadline by supplying a client; everything else, from the root CAs to a
	// proxy, is the caller's to choose.
	HTTPClient *http.Client
}

// Client calls the endpoints of a single Data App.
type Client struct {
	baseURL    string
	publicKey  string
	privateKey string
	http       *http.Client
}

// Validate reports whether the configuration can be used, without building a
// client or making a call.
//
// It exists so a missing key is a startup failure with a clear message rather
// than a 401 from an endpoint the first time an admin clicks a button. That is
// the same contract the rest of the secrets in this app have.
func (c Config) Validate() error {
	if c.BaseURL == "" {
		return errors.New("TIDB_DS_BASE_URL is required")
	}
	if c.PublicKey == "" || c.PrivateKey == "" {
		return errors.New("TIDB_DS_PUBLIC_KEY and TIDB_DS_PRIVATE_KEY must be set together")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("TIDB_DS_BASE_URL is not a URL: %w", err)
	}
	if u.Scheme != "https" {
		// The credential is base64, not encryption. Sending it over plain http
		// would hand it to anyone on the path.
		return fmt.Errorf("TIDB_DS_BASE_URL must be https, got %q", u.Scheme)
	}
	return nil
}

// New builds a client. It refuses a plaintext base URL on purpose: the
// credentials travel as base64, which is encoding and not encryption, so https
// is the only thing standing between the private key and a network observer.
func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("dataservice: parse base URL: %w", err)
	}
	if u.Host == "" {
		return nil, errors.New("dataservice: base URL has no host")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	// The transport is copied rather than used in place, so filling in the
	// deadline here cannot reach back and mutate a client the caller still owns.
	var transport http.Client
	if cfg.HTTPClient != nil {
		transport = *cfg.HTTPClient
	}
	transport.Timeout = timeout

	return &Client{
		baseURL:    strings.TrimRight(u.String(), "/"),
		publicKey:  cfg.PublicKey,
		privateKey: cfg.PrivateKey,
		http:       &transport,
	}, nil
}

// Column describes one field of the result set. Data Service sends it alongside
// the rows rather than leaving the caller to guess the shape.
type Column struct {
	Name     string `json:"col"`
	DataType string `json:"data_type"`
	Nullable bool   `json:"nullable"`
}

// Result is the execution report of the endpoint's last statement.
type Result struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RowCount  int    `json:"row_count"`
	RowAffect int    `json:"row_affect"`
	Limit     int    `json:"limit"`
}

// Envelope is the body every Data Service endpoint returns, success or failure.
type Envelope struct {
	Type string `json:"type"`
	Data struct {
		Columns []Column `json:"columns"`
		Rows    []Row    `json:"rows"`
		Result  Result   `json:"result"`
	} `json:"data"`
}

// Row is one result row, keyed by column name.
//
// Every value arrives as a string, including numbers: an id comes back as
// "2083456789012345" rather than a JSON number. The accessors below coerce and
// report a mismatch rather than quietly yielding zero, because a book priced at
// 0 cents is a bug that would otherwise reach a customer.
type Row map[string]any

// Str returns a column as text, mapping null and absent to "".
func (r Row) Str(name string) string {
	v, ok := r[name]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

// NullableStr returns a column and whether it held anything. It exists for the
// nullable columns of the catalogue, where "" and NULL are different values.
func (r Row) NullableStr(name string) (string, bool) {
	s := r.Str(name)
	return s, s != ""
}

// Int64 coerces a column to an integer.
func (r Row) Int64(name string) (int64, error) {
	s := r.Str(name)
	if s == "" {
		return 0, fmt.Errorf("dataservice: column %q is null or empty, want an integer", name)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("dataservice: column %q holds %q, want an integer: %w", name, s, err)
	}
	return n, nil
}

// Int coerces a column to an int, rejecting anything that does not fit.
//
// The range check is not ceremony: the shop ids are AUTO_RANDOM BIGINTs, which
// are far wider than an int, and a 32-bit build would silently truncate them
// into a different book. It cannot be exercised on a 64-bit test machine, where
// every int64 is a valid int.
func (r Row) Int(name string) (int, error) {
	n, err := r.Int64(name)
	if err != nil {
		return 0, err
	}
	i := int(n)
	if int64(i) != n {
		return 0, fmt.Errorf("dataservice: column %q holds %d, out of range for int", name, n)
	}
	return i, nil
}

// Error is a failed call, carrying both failure channels.
//
// They are not redundant. Data Service answers HTTP 200 with the failure in
// data.result.code, so a caller that only looked at the status would report a
// rejected DELETE as a success. Code is the TiDB error number when the endpoint
// got far enough to run SQL, and 0 when the request never did.
type Error struct {
	Method  string
	Path    string
	Status  int
	Code    int
	Message string
}

func (e *Error) Error() string {
	code := "none"
	if e.Code != 0 {
		code = strconv.Itoa(e.Code)
	}
	return fmt.Sprintf("dataservice: %s %s: status %d code %s: %s",
		e.Method, e.Path, e.Status, code, e.Message)
}

// IsDuplicate reports a unique index rejection, so a repeated ISBN is a
// conflict rather than a server fault.
func (e *Error) IsDuplicate() bool { return e.Code == codeDuplicateEntry }

// IsForeignKey reports a rejected reference, which in this schema means either
// deleting a book that still appears in an order (1451) or pointing a child row
// at a book that is gone (1452).
func (e *Error) IsForeignKey() bool {
	return e.Code == codeParentHasChildren || e.Code == codeChildHasNoParent
}

// Query runs an endpoint and returns its rows.
//
// method is GET or DELETE, whose parameters travel in the query string, or
// POST and PUT, whose parameters travel in a JSON body. Data Service fixes that
// mapping per verb; it is not a choice the caller makes.
func (c *Client) Query(ctx context.Context, method, path string, params map[string]any) ([]Row, error) {
	env, err := c.send(ctx, method, path, params)
	if err != nil {
		return nil, err
	}
	return env.Data.Rows, nil
}

// QueryOne runs an endpoint expected to match at most one row.
//
// An empty result is sql.ErrNoRows rather than an error of its own, because that
// is what the sqlc-backed store returns and what the handlers already translate
// into 404. Reusing the sentinel keeps a missing book a 404 whether it was
// looked up over SQL or over HTTP.
func (c *Client) QueryOne(ctx context.Context, method, path string, params map[string]any) (Row, error) {
	rows, err := c.Query(ctx, method, path, params)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return rows[0], nil
}

// Exec runs an endpoint that writes and returns its execution report.
//
// It exists because a writing endpoint has no rows to hand back. An INSERT, an
// UPDATE or a DELETE answers with the number of rows it changed in row_affect,
// and that count is the only evidence the statement did anything, which Query
// throws away. A caller that writes and then has to know whether it landed needs
// it, and an endpoint that silently changed nothing must not be read as success.
func (c *Client) Exec(ctx context.Context, method, path string, params map[string]any) (Result, error) {
	env, err := c.send(ctx, method, path, params)
	if err != nil {
		return Result{}, err
	}
	return env.Data.Result, nil
}

// send performs one call and returns the envelope only if it really succeeded.
func (c *Client) send(ctx context.Context, method, path string, params map[string]any) (*Envelope, error) {
	clean := strings.Trim(path, "/")
	if clean == "" {
		return nil, errors.New("dataservice: endpoint path is required")
	}

	var body io.Reader
	target := c.baseURL + "/" + clean

	switch method {
	case http.MethodGet, http.MethodDelete:
		if len(params) > 0 {
			q := url.Values{}
			for k, v := range params {
				q.Set(k, fmt.Sprint(v))
			}
			target += "?" + q.Encode()
		}
	case http.MethodPost, http.MethodPut:
		// An endpoint without batch operation accepts a single object, so the
		// body is the parameter map itself and never wrapped in "items".
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("dataservice: encode parameters for %s %s: %w", method, clean, err)
		}
		body = bytes.NewReader(encoded)
	default:
		return nil, fmt.Errorf("dataservice: unsupported method %q", method)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("dataservice: build request: %w", err)
	}
	req.SetBasicAuth(c.publicKey, c.privateKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dataservice: call %s %s: %w", method, clean, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("dataservice: read response of %s %s: %w", method, clean, err)
	}

	var env Envelope
	decodeErr := json.Unmarshal(raw, &env)

	// The status is checked first, and a body that will not decode is reported
	// as whatever the status was rather than as a parse error: a proxy or a
	// gateway can answer 502 with HTML, and "status 502" is the useful fact.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &Error{Method: method, Path: clean, Status: resp.StatusCode,
			Code: env.Data.Result.Code, Message: messageOf(env, decodeErr, raw)}
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("dataservice: decode response of %s %s: %w", method, clean, decodeErr)
	}
	if env.Data.Result.Code != 0 && env.Data.Result.Code != http.StatusOK {
		return nil, &Error{Method: method, Path: clean, Status: resp.StatusCode,
			Code: env.Data.Result.Code, Message: env.Data.Result.Message}
	}
	return &env, nil
}

// messageOf digs a human-readable reason out of a failure body.
func messageOf(env Envelope, decodeErr error, raw []byte) string {
	if decodeErr == nil && env.Data.Result.Message != "" {
		return env.Data.Result.Message
	}
	if s := strings.TrimSpace(string(raw)); s != "" {
		return s
	}
	return http.StatusText(env.Data.Result.Code)
}
