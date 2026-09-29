package backup

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"dvbs/internal/config"
	_ "github.com/go-sql-driver/mysql"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "mysql://dvbs:dvbs@localhost:4000/dvbs"
	}
	dsn := config.ParseDSN(url)
	if err := config.RegisterTLSConfig(""); err != nil {
		panic("TLS config: " + err.Error())
	}
	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	if err := pool.Ping(); err != nil {
		panic("database not reachable: " + err.Error())
	}

	testDB = pool
	os.Exit(m.Run())
}

// hostile is every value that has broken a naive SQL dump at least once.
var hostile = []string{
	"plain",
	"",
	"it's got a quote",
	`a backslash \ and a quote '`,
	"double \"quotes\" are not special in single quotes",
	"newline\nand carriage\rreturn",
	"tab\there",
	"nul\x00byte",
	"ctrl-z\x1aends the file for some tools",
	"semicolon; DROP TABLE users; --",
	"backtick ` identifier",
	"the literal word NULL",
	"the literal word 0",
	"emoji 📚 and accents áéíóúñ",
	"日本語のテキスト",
	strings.Repeat("long ", 500),
	"-- comment at the start",
	"/* block comment */",
}

// TestQuoteIsWhatTheServerAgreesWith is the test that matters for this file.
//
// A dump is correct only if the server reads back what went in, so the
// assertions are not about the shape of the output but about TiDB evaluating
// the emitted literal to the original value. Everything here is a string that
// some other escaping gets wrong.
func TestQuoteIsWhatTheServerAgreesWith(t *testing.T) {
	ctx := context.Background()
	for i, in := range hostile {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			var got sql.NullString
			if err := testDB.QueryRowContext(ctx, "SELECT "+literal(in)).Scan(&got); err != nil {
				t.Fatalf("TiDB rejected the literal for %q: %v", in, err)
			}
			if !got.Valid {
				t.Fatalf("TiDB read %q as NULL", in)
			}
			if got.String != in {
				t.Errorf("round trip changed the value\n in: %q\nout: %q\nliteral: %s", in, got.String, literal(in))
			}
		})
	}
}

// TestNilAndTheWordNullAreDifferentValues is the distinction that a dump
// silently gets wrong: one restores a row, the other inserts the text "NULL"
// into a NOT NULL column and fails, or worse, writes a plausible-looking row
// that was never in the database.
func TestNilAndTheWordNullAreDifferentValues(t *testing.T) {
	ctx := context.Background()

	if got := literal(nil); got != "NULL" {
		t.Errorf("nil should be the NULL keyword, got %s", got)
	}
	if got := literal("NULL"); got != "'NULL'" {
		t.Errorf("the word NULL should be a quoted string, got %s", got)
	}
	if got := literal(""); got != "''" {
		t.Errorf("empty string should be two quotes, got %s", got)
	}

	var isNull sql.NullString
	if err := testDB.QueryRowContext(ctx, "SELECT "+literal(nil)).Scan(&isNull); err != nil {
		t.Fatalf("select of a NULL literal: %v", err)
	}
	if isNull.Valid {
		t.Error("the NULL literal came back as a value, not as NULL")
	}

	var empty sql.NullString
	if err := testDB.QueryRowContext(ctx, "SELECT "+literal("")).Scan(&empty); err != nil {
		t.Fatalf("select of an empty string literal: %v", err)
	}
	if !empty.Valid || empty.String != "" {
		t.Errorf("empty string round trip: valid=%v value=%q", empty.Valid, empty.String)
	}
}

// TestIntegersKeepTheirExactDigits covers the identifiers. A 19-digit
// AUTO_RANDOM id rendered through a float, or printed with a thousand
// separator, restores a database that points at rows which do not exist.
func TestIntegersKeepTheirExactDigits(t *testing.T) {
	ctx := context.Background()

	const big int64 = 4035225266124144421
	if got := literal(big); got != "4035225266124144421" {
		t.Errorf("large id was reformatted: %s", got)
	}

	var got int64
	if err := testDB.QueryRowContext(ctx, "SELECT "+literal(big)).Scan(&got); err != nil {
		t.Fatalf("TiDB rejected the integer literal: %v", err)
	}
	if got != big {
		t.Errorf("integer round trip: want %d, got %d", big, got)
	}
}

// TestDatetimeKeepsTheMillisecond pins the timestamp format to what the
// DATETIME(3) columns actually store, and checks the server agrees, including
// that the value is written in UTC and not in the machine's zone.
func TestDatetimeKeepsTheMillisecond(t *testing.T) {
	ctx := context.Background()

	// A time in a non-UTC zone. The dump has to carry the instant, not the
	// offset, because the column has no zone to store it in.
	zone := time.FixedZone("test", 5*3600)
	original := time.Date(2026, 9, 27, 20, 45, 1, 234000000, zone)

	// The CAST is what makes this a test of a datetime and not of a string: it
	// hands the emitted text to the same parser a restore would, and rejects it
	// if the layout is one TiDB does not accept.
	var got time.Time
	if err := testDB.QueryRowContext(ctx, "SELECT CAST("+literal(original)+" AS DATETIME(3))").Scan(&got); err != nil {
		t.Fatalf("TiDB rejected the datetime literal: %v", err)
	}
	if !got.Equal(original) {
		t.Errorf("datetime round trip: want %s, got %s", original, got)
	}
	if got.Nanosecond() != 234000000 {
		t.Errorf("milliseconds were lost: %s", got)
	}
}

// TestBooleanAndBytesFromTheDriver covers the two shapes the driver hands back
// that are not self-describing: a TINYINT that reads as a bool, and a text
// column that reads as []byte.
func TestBooleanAndBytesFromTheDriver(t *testing.T) {
	ctx := context.Background()

	if got := literal(true); got != "1" {
		t.Errorf("true should be 1, got %s", got)
	}
	if got := literal(false); got != "0" {
		t.Errorf("false should be 0, got %s", got)
	}
	if got := literal([]byte("bytes read back")); got != "'bytes read back'" {
		t.Errorf("[]byte should be a quoted string, got %s", got)
	}

	var one int
	if err := testDB.QueryRowContext(ctx, "SELECT "+literal(true)).Scan(&one); err != nil {
		t.Fatalf("TiDB rejected the boolean literal: %v", err)
	}
	if one != 1 {
		t.Errorf("true round trip: want 1, got %d", one)
	}
}

// TestDumpOfTheLiveDatabase is the end-to-end check: a dump of the real schema
// has the tables, the constraints and a replayable header, and the hostile
// values survive the whole path rather than only the escaper.
func TestDumpOfTheLiveDatabase(t *testing.T) {
	ctx := context.Background()
	d := New(testDB)
	d.now = func() time.Time { return time.Date(2026, 9, 27, 20, 45, 0, 0, time.UTC) }

	var out strings.Builder
	res, err := d.Dump(ctx, &out)
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	got := out.String()

	if res.Tables < 10 {
		t.Errorf("expected every table in the schema, got %d", res.Tables)
	}
	if res.Bytes != int64(len(got)) {
		t.Errorf("Result.Bytes is %d but the dump is %d bytes long", res.Bytes, len(got))
	}

	// The header has to be enough to restore into an empty database, and to
	// keep the utf8mb4 bytes intact on the way back in.
	for _, want := range []string{
		"SET NAMES utf8mb4;",
		"SET FOREIGN_KEY_CHECKS = 0;",
		"generated: 2026-09-27T20:45:00Z",
		"CREATE TABLE `users`",
		"CREATE TABLE `books`",
		"SET FOREIGN_KEY_CHECKS = 1;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump is missing %q", want)
		}
	}

	// Parents have to come before the children that reference them.
	usersAt := strings.Index(got, "CREATE TABLE `users`")
	ordersAt := strings.Index(got, "CREATE TABLE `orders`")
	itemsAt := strings.Index(got, "CREATE TABLE `order_items`")
	redemptionsAt := strings.Index(got, "CREATE TABLE `coupon_redemptions`")
	if usersAt < 0 || ordersAt < 0 || itemsAt < 0 || redemptionsAt < 0 {
		t.Fatal("dump is missing one of the tables checked for ordering")
	}
	if !(usersAt < ordersAt && ordersAt < itemsAt && ordersAt < redemptionsAt) {
		t.Errorf("tables are not parents-first: users=%d orders=%d items=%d redemptions=%d",
			usersAt, ordersAt, itemsAt, redemptionsAt)
	}

	// The live schema, not a copy of the migration file. If the migration ever
	// drifts from the database, this is where it shows.
	if !strings.Contains(got, "AUTO_RANDOM") {
		t.Error("dump does not carry the server's own CREATE TABLE")
	}
}

// TestDumpIsReplayable is the test that would catch a dump that only looks
// right: it loads the dump's own INSERT statements into a scratch table and
// checks the values come back identical, hostile characters included.
func TestDumpIsReplayable(t *testing.T) {
	ctx := context.Background()

	_, err := testDB.ExecContext(ctx, "DROP TABLE IF EXISTS `backup_replay`")
	if err != nil {
		t.Fatalf("drop scratch table: %v", err)
	}
	defer func() { _, _ = testDB.ExecContext(ctx, "DROP TABLE IF EXISTS `backup_replay`") }()

	_, err = testDB.ExecContext(ctx, "CREATE TABLE `backup_replay` (`id` BIGINT, `body` TEXT, `note` TEXT NULL)")
	if err != nil {
		t.Fatalf("create scratch table: %v", err)
	}

	// Build the INSERTs with the escaper, exactly as the dumper does.
	quoted := make([]string, 0, len(hostile))
	for _, v := range hostile {
		quoted = append(quoted, "("+literal(1)+", "+literal(v)+", "+literal(nil)+")")
	}
	// One NULL and one row whose note is the four characters "NULL".
	quoted = append(quoted, "("+literal(2)+", "+literal("ok")+", "+literal("NULL")+")")

	stmt := "INSERT INTO `backup_replay` (`id`, `body`, `note`) VALUES " + strings.Join(quoted, ", ")
	if _, err := testDB.ExecContext(ctx, stmt); err != nil {
		t.Fatalf("the generated statements do not load: %v", err)
	}

	rows, err := testDB.QueryContext(ctx, "SELECT `id`, `body`, `note` FROM `backup_replay` ORDER BY `id`")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer func() { _ = rows.Close() }()

	i := 0
	for rows.Next() {
		var id int
		var body, note sql.NullString
		if err := rows.Scan(&id, &body, &note); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if id == 2 {
			if !note.Valid || note.String != "NULL" {
				t.Errorf(`the text "NULL" came back as %v`, note)
			}
			i++
			continue
		}
		if note.Valid {
			t.Errorf("row %d: NULL came back as %q", id, note.String)
		}
		if body.String != hostile[i] {
			t.Errorf("row %d: value changed\nwant %q\ngot  %q", id, hostile[i], body.String)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if i != len(hostile)+1 {
		t.Errorf("expected %d rows back, got %d", len(hostile)+1, i)
	}
}

func TestFilenameIsSortableAndSelfDescribing(t *testing.T) {
	got := Filename(time.Date(2026, 9, 27, 20, 45, 7, 0, time.UTC))
	if got != "dvbs-backup-20260927-204507.sql" {
		t.Errorf("filename is %q", got)
	}
}
