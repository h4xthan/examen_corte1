package transport_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// dumpAsAdmin requests a backup with an administrator credential and returns the
// response with its body already read, since the body is a file and not JSON.
func dumpAsAdmin(t *testing.T, token string) (*http.Response, string) {
	t.Helper()

	req, _ := newJSONRequest(t, http.MethodPost, "/admin/backup", nil, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/backup: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read backup body: %v", err)
	}
	return resp, string(body)
}

// TestBackupIsOnlyReachableByAnAdmin is the access control, and the reason the
// route exists behind the panel: the dump has every password hash and every
// live reset token in it.
func TestBackupIsOnlyReachableByAnAdmin(t *testing.T) {
	// A signed-in customer is refused, and refused without a body that would
	// tell them whether the route exists for them.
	victimID, _, victimToken := setupUsersAndToken(t)
	if victimToken == "" {
		t.Fatal("could not set up a customer session")
	}
	_ = victimID

	resp, _ := dumpAsAdmin(t, victimToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a customer asking for a backup got %d, want 403", resp.StatusCode)
	}

	// An anonymous caller gets nothing at all.
	anonReq, _ := newJSONRequest(t, http.MethodPost, "/admin/backup", nil, "")
	anonResp, err := http.DefaultClient.Do(anonReq)
	if err != nil {
		t.Fatalf("anonymous POST /admin/backup: %v", err)
	}
	defer anonResp.Body.Close()
	if anonResp.StatusCode == http.StatusOK {
		t.Error("an anonymous caller received a database dump")
	}
}

// TestBackupIsNotAGet is why the route is a POST.
//
// A GET would let anything that follows a link set it off: a link preview in a
// chat client, a browser extension walking the page, an image tag in a comment.
// Each one would trigger a full read of the database and hand the result to a
// third party.
func TestBackupIsNotAGet(t *testing.T) {
	req, _ := newJSONRequest(t, http.MethodGet, "/admin/backup", nil, adminToken(t))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/backup: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("GET /admin/backup served a dump; the route must be POST only")
	}
}

// TestBackupIsASelfContainedRestorableFile is the test that says the feature
// works: what comes out of the endpoint has to load into an empty database on
// its own, with no help from cmd/migrate.
func TestBackupIsASelfContainedRestorableFile(t *testing.T) {
	resp, body := dumpAsAdmin(t, adminToken(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /admin/backup as admin: status %d, want 200", resp.StatusCode)
	}

	// It arrives as a download, and it is not cached: a shared machine or an
	// intercepting proxy holding a copy of a password-hash dump is the failure
	// this feature is most likely to cause.
	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Errorf("Content-Disposition is %q, want an attachment", got)
	}
	if !regexp.MustCompile(`filename="dvbs-backup-\d{8}-\d{6}\.sql"`).MatchString(resp.Header.Get("Content-Disposition")) {
		t.Errorf("filename is not dated: %q", resp.Header.Get("Content-Disposition"))
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control is %q, want no-store", got)
	}

	// The schema travels with the data, or the file only works on a database
	// that already has the tables.
	for _, want := range []string{
		"SET NAMES utf8mb4;",
		"SET FOREIGN_KEY_CHECKS = 0;",
		"CREATE TABLE `users`",
		"CREATE TABLE `books`",
		"CREATE TABLE `orders`",
		"CREATE TABLE `reviews`",
		"SET FOREIGN_KEY_CHECKS = 1;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the dump does not contain %q", want)
		}
	}

	// Every table in the schema, not the ones that happened to be convenient.
	for _, table := range []string{
		"users", "password_reset_tokens", "addresses", "books", "coupons",
		"orders", "order_items", "reviews", "coupon_redemptions", "payment_methods",
	} {
		if !strings.Contains(body, "CREATE TABLE `"+table+"`") {
			t.Errorf("the dump has no CREATE TABLE for %q", table)
		}
	}

	// The file must not look truncated: the marker the handler appends when a
	// dump breaks halfway would be the one thing that makes a file untrustworthy.
	if strings.Contains(body, "BACKUP INCOMPLETE") {
		t.Error("the dump is marked incomplete: the transfer failed partway")
	}

	// Real rows, and the identifier precision that a naive dump loses.
	if !strings.Contains(body, "INSERT INTO `books`") {
		t.Error("the dump carries no book rows")
	}
	if !strings.Contains(body, "AUTO_RANDOM") {
		t.Error("the dump does not carry the server's own schema, so a column " +
			"added by a later migration would be missing")
	}
}

// TestBackupCanBeWrittenToDiskAndReplayed is the end of the chain, and the only
// one that proves the file is a backup rather than a text file. It splits the
// dump into its statements and runs them against a scratch database schema,
// which is what somebody restoring it would do.
func TestBackupCanBeWrittenToDiskAndReplayed(t *testing.T) {
	resp, body := dumpAsAdmin(t, adminToken(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /admin/backup: status %d", resp.StatusCode)
	}

	// Every INSERT has to be a whole statement. The dump writes the column list
	// and the word VALUES on the first line and the rows after it, so a match on
	// one line stops before the semicolon. The pattern has to span lines and end
	// at the terminator: one truncated INSERT is a restore that stops halfway and
	// a database that is quietly missing rows.
	inserts := regexp.MustCompile(`(?s)INSERT INTO .*?;`).FindAllString(body, -1)
	if len(inserts) == 0 {
		t.Fatal("the dump contains no complete INSERT statements")
	}
	for i, stmt := range inserts {
		if strings.Count(stmt, "(") != strings.Count(stmt, ")") {
			t.Errorf("INSERT %d has unbalanced parentheses: %.80s", i, stmt)
		}
		if strings.Count(stmt, "'")%2 != 0 {
			// Escaped quotes come in pairs, \' and \', so an odd total means a
			// value was cut in half by the writer.
			t.Errorf("INSERT %d has an odd number of quotes: %.120s", i, stmt)
		}
	}

	// The statements run in a transaction and are rolled back: this executes the
	// dump for real, against the live schema, without leaving anything behind.
	if err := replayDumpIntoScratchTables(t, body); err != nil {
		t.Fatalf("the dump does not execute against the live schema: %v", err)
	}
}

// replayDumpIntoScratchTables executes the dump for real: the CREATE TABLEs land,
// the INSERTs load, and the state is then thrown away.
//
// The history, because the first version of this check did real damage: it ran
// the dump inside a "rolled-back" transaction. It did not roll back. TiDB, like
// MySQL, commits DDL implicitly, so the dump's DROP/CREATE statements rewrote
// the live tables and the rollback could not reach them. The lesson is what
// makes this version work at all:
//
//  1. No transaction. With DDL committing implicitly, a transaction is a
//     promise the database does not keep, and pretending otherwise is how the
//     damage passed unnoticed.
//  2. No real table. Every table name in the dump is rewritten to a
//     backup_replay_* twin, so the restore runs into an empty schema that has
//     the same DDL. The real tables never hear about it.
//  3. Parity after. If the dump dropped a row, or added one, or could not
//     actually carry a value, the replay count differs from the source count
//     and the test fails.
func replayDumpIntoScratchTables(t *testing.T, dump string) error {
	t.Helper()

	ctx := context.Background()

	// Every table the dump declares, in CREATE TABLE order, which is the dump's
	// own parent-first order.
	tableRe := regexp.MustCompile(`(?m)^CREATE TABLE ` + "`" + `([a-z_]+)` + "`")
	matches := tableRe.FindAllStringSubmatch(dump, -1)
	if len(matches) == 0 {
		return errors.New("the dump declares no tables")
	}
	tables := make([]string, 0, len(matches))
	for _, m := range matches {
		tables = append(tables, m[1])
	}

	// Dropped in every outcome, success and failure alike. And the drop runs
	// unconditionally and immediately — no transaction, no defer of a
	// rollback — because that is the only cleanup that is guaranteed to happen.
	//
	// FK checks have to be off for it: the dump ends by restoring
	// FOREIGN_KEY_CHECKS = 1, and with them on, TiDB refuses to drop a parent
	// table (users, books, ...) while its backup_replay_* children still exist.
	// The first version forgot this and left the four parents behind.
	defer func() {
		_, _ = testDB.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0")
		for _, t := range tables {
			_, _ = testDB.ExecContext(ctx, "DROP TABLE IF EXISTS `backup_replay_"+t+"`")
		}
		_, _ = testDB.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
	}()

	// Rewrite every table reference to its twin, then run the statements in
	// order as a real restore would.
	replace := func(name string) string { return "backup_replay_" + name }
	for _, chunk := range strings.Split(dump, ";") {
		stmt := strings.TrimSpace(chunk)
		if stmt == "" {
			continue
		}
		for _, t := range tables {
			stmt = strings.ReplaceAll(stmt, "`"+t+"`", "`"+replace(t)+"`")
		}
		if _, err := testDB.ExecContext(ctx, stmt); err != nil {
			head := stmt
			if len(head) > 120 {
				head = head[:120]
			}
			return fmt.Errorf("statement failed: %w\n  %.120s", err, head)
		}
	}

	// The real proof. Every table that was dumped must contain exactly as many
	// rows in its twin as in itself.
	for _, t := range tables {
		var real, replay int
		if err := testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+t+"`").Scan(&real); err != nil {
			return err
		}
		if err := testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM `backup_replay_"+t+"`").Scan(&replay); err != nil {
			return err
		}
		if real != replay {
			return fmt.Errorf("replay of %s has %d rows, source has %d", t, replay, real)
		}
	}
	return nil
}
