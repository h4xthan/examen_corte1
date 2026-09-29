// Package backup produces a logical dump of the database: the real schema
// followed by the real rows, in SQL that can be replayed into an empty TiDB.
//
// It exists because TiDB Cloud Starter does not support the BACKUP and RESTORE
// statements at all — not for a lack of privileges, but as a feature that is
// absent from the managed tiers. The documentation is explicit that backups on
// Starter are taken by the platform, on a schedule, and restored from the
// console. So the only backup a program can produce on this tier is the one it
// reads out itself, and that is what this is.
//
// What this is not: not a snapshot in the storage engine's sense. It is a
// consistent read of every table taken inside one transaction, which is the
// strongest guarantee available from the outside. The daily managed backup is
// still the real safety net, and this is the one you can get without leaving the
// application.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// insertBatch is how many row tuples go into a single INSERT.
//
// A single statement carrying the whole table is a single point of failure: one
// truncated transfer and nothing lands. Batching means a dump is worth
// restoring even if it stops early, and it keeps the statements inside
// max_allowed_packet on a large table.
const insertBatch = 200

// datetimeLayout is how a DATETIME(3) column is written.
//
// The columns have no time zone, so a timestamp is stored as UTC. Writing
// t.UTC() rather than the driver's own offset is what makes the dump reload into
// the same value it was read from, whatever the machine that produced it.
const datetimeLayout = "2006-01-02 15:04:05.000"

// tableOrder lists the tables parents-first, so the dump also loads into a
// database that has FOREIGN_KEY_CHECKS on.
//
// FOREIGN_KEY_CHECKS is disabled in the header anyway, so this is belt and
// braces, but a dump that loads in order is a dump somebody can read.
var tableOrder = []string{
	"users",
	"books",
	"coupons",
	"orders",
	"password_reset_tokens",
	"addresses",
	"payment_methods",
	"order_items",
	"reviews",
	"coupon_redemptions",
}

// Dumper writes a logical dump of one database to a writer.
type Dumper struct {
	db *sql.DB
	// now is the clock used for the header stamp. A field rather than a call to
	// time.Now so the output is testable.
	now func() time.Time
}

// New returns a Dumper over db.
func New(db *sql.DB) *Dumper {
	return &Dumper{db: db, now: time.Now}
}

// Result is what a completed dump contained, for the audit log and the header.
type Result struct {
	Tables int
	Rows   int64
	// Bytes is the size of the SQL written, not of the data before encoding.
	Bytes int64
}

// Dump writes the whole database to w.
//
// The read happens inside one transaction. TiDB serves REPEATABLE READ reads
// from a single snapshot taken at the first statement of the transaction, so
// every table in the dump is the database as it was at that instant. Without
// the transaction, a book row and the review that points at it could be read on
// opposite sides of a write and the dump would not describe any moment in
// time that ever existed.
func (d *Dumper) Dump(ctx context.Context, w io.Writer) (Result, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin transaction: %w", err)
	}
	// The dump only reads. A rollback is the correct way to close a read-only
	// transaction, and it is what the deferred rollback does when commit fails.
	defer func() { _ = tx.Rollback() }()

	tables, err := d.tables(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if len(tables) == 0 {
		return Result{}, errors.New("no tables found in the database")
	}

	cw := &countingWriter{w: w}
	var res Result

	stamp := d.now().UTC().Format(time.RFC3339)
	// SET NAMES first: without it the loader's client charset decides how these
	// bytes are read back, and a utf8mb4 database loaded under latin1 is
	// corrupted in a way nothing in the file reveals.
	fmt.Fprintf(cw, "-- dvbs logical dump\n-- generated: %s\n", stamp)
	fmt.Fprintf(cw, "-- source: %s\n", d.dbName(ctx, tx))
	fmt.Fprintf(cw, "--\n-- Restore with:\n--   mysql -u <user> -p <database> < this-file.sql\n")
	fmt.Fprintf(cw, "-- It is self-contained: it carries the schema, so it does not need cmd/migrate.\n\n")
	fmt.Fprintf(cw, "SET NAMES utf8mb4;\n")
	fmt.Fprintf(cw, "SET FOREIGN_KEY_CHECKS = 0;\n")
	// Every primary key in this schema is AUTO_RANDOM, and TiDB refuses an
	// explicit insert into an AUTO_RANDOM column unless this is on: error 8216,
	// "Invalid auto random: Explicit insertion on auto_random column is
	// disabled". Without this line the dump carries the ids and the restore
	// stops at the first row, which is how a backup turns out to be unusable at
	// the exact moment somebody needs it.
	fmt.Fprintf(cw, "SET @@allow_auto_random_explicit_insert = true;\n\n")

	for _, table := range tables {
		if err := d.writeTable(ctx, tx, cw, table, &res); err != nil {
			return res, fmt.Errorf("table %s: %w", table, err)
		}
	}

	fmt.Fprintf(cw, "\nSET FOREIGN_KEY_CHECKS = 1;\n")
	res.Bytes = cw.n
	return res, tx.Commit()
}

// writeTable emits the CREATE TABLE for one table and then its rows.
func (d *Dumper) writeTable(ctx context.Context, tx *sql.Tx, w io.Writer, table string, res *Result) error {
	create, err := createStatement(ctx, tx, table)
	if err != nil {
		return err
	}

	// DROP before CREATE, so replaying the dump over a populated database
	// replaces the contents instead of failing on the first duplicate key.
	// A restore into an empty database ignores it.
	fmt.Fprintf(w, "\n-- ---- %s ----\n", table)
	fmt.Fprintf(w, "DROP TABLE IF EXISTS `%s`;\n", table)
	fmt.Fprintf(w, "%s;\n", create)

	cols, err := insertableColumns(ctx, tx, table)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return errors.New("table has no insertable columns")
	}

	columnList := make([]string, len(cols))
	selectList := make([]string, len(cols))
	for i, c := range cols {
		columnList[i] = "`" + c + "`"
		selectList[i] = "`" + c + "`"
	}
	prefix := "INSERT INTO `" + table + "` (" + strings.Join(columnList, ", ") + ") VALUES\n"

	// scan holds one reusable destination per column. Allocating []any per row
	// would put a slice header on the heap for every column of every row, and
	// a dump is exactly the workload where that shows up.
	scan := make([]any, len(cols))
	holders := make([]any, len(cols))
	for i := range scan {
		holders[i] = &scan[i]
	}

	var (
		tuples []string
		count  int64
	)
	flush := func() error {
		if len(tuples) == 0 {
			return nil
		}
		if _, err := fmt.Fprint(w, prefix+strings.Join(tuples, ",\n")+";\n"); err != nil {
			return err
		}
		tuples = tuples[:0]
		return nil
	}

	// The projection names the columns rather than using SELECT * precisely so
	// the scan and the INSERT agree. A generated column has to be left out of
	// both: TiDB rejects an explicit value for one (error 3105), and the CREATE
	// TABLE above already recomputes it.
	rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(selectList, ", ")+" FROM `"+table+"`")
	for rows.Next() {
		if err := rows.Scan(holders...); err != nil {
			return err
		}
		parts := make([]string, len(scan))
		for i, v := range scan {
			parts[i] = literal(v)
		}
		tuples = append(tuples, "("+strings.Join(parts, ", ")+")")
		count++
		if len(tuples) >= insertBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}

	res.Tables++
	res.Rows += count
	return nil
}

// tables lists the database's tables, known ones in foreign-key order and any
// table added since tableOrder was written after them, alphabetically.
//
// Reading the list from the database is what keeps a future migration from
// producing a dump that silently omits a table. The alternative — a hardcoded
// list — fails in the direction nobody notices.
func (d *Dumper) tables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT TABLE_NAME FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []string
	for _, t := range tableOrder {
		if present[t] {
			out = append(out, t)
			delete(present, t)
		}
	}
	extra := make([]string, 0, len(present))
	for t := range present {
		extra = append(extra, t)
	}
	sort.Strings(extra)
	return append(out, extra...), nil
}

// dbName is the header's provenance line, so a dump says where it came from.
func (d *Dumper) dbName(ctx context.Context, tx *sql.Tx) string {
	var name sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name); err != nil {
		return "unknown"
	}
	if !name.Valid || name.String == "" {
		return "unknown"
	}
	return name.String
}

// createStatement asks the server for the real DDL rather than shipping a copy
// of the migration.
//
// A dump is only worth restoring if its schema is the schema that produced the
// rows. Reading it from the server means a column added by a migration the
// dump code never heard of is in the output, and a discrepancy between the
// migration file and the live database is visible here instead of at 3am.
func createStatement(ctx context.Context, tx *sql.Tx, table string) (string, error) {
	var name, ddl sql.NullString
	err := tx.QueryRowContext(ctx, "SHOW CREATE TABLE `"+table+"`").Scan(&name, &ddl)
	if err != nil {
		return "", fmt.Errorf("show create table: %w", err)
	}
	if !ddl.Valid || ddl.String == "" {
		return "", errors.New("show create table returned no statement")
	}
	return ddl.String, nil
}

// insertableColumns lists a table's columns in ordinal order, skipping the
// generated ones.
//
// A generated column is computed by the server from the others, and TiDB
// refuses an explicit value for it: "The value specified for generated column
// is not allowed", error 3105. A dump that read SELECT * and named every column
// would carry the generated value and be rejected at the first insert — which
// is the shape of a backup that only fails when somebody tries to use it.
//
// The order comes from ORDINAL_POSITION so the projection matches the table as
// declared, and the column list is explicit so a dump stays loadable if the
// table is altered afterwards.
func insertableColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT COLUMN_NAME FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		   AND EXTRA NOT LIKE '%GENERATED%'
		 ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		return nil, fmt.Errorf("list columns of %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// literal renders one column value as a SQL literal.
//
// The distinction it exists to preserve: nil is the keyword NULL, and the
// four-character string "NULL" is a quoted string. They are different values and
// only one of them restores a row that was actually there.
func literal(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if t {
			return "1"
		}
		return "0"
	case time.Time:
		return quote(t.UTC().Format(datetimeLayout))
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		// 'g' with precision -1 gives the shortest text that reads back to the
		// same value. fmt's default would pick a fixed exponent and lose the
		// last digit on a number that is supposed to be money.
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []byte:
		// The driver hands back bytes for several column types even when they
		// are text. Quoting the bytes as a string literal is right for all of
		// them; a hex literal would be wrong for a varchar.
		return quote(string(t))
	case string:
		return quote(t)
	default:
		// A type added to a column later than this code: writing it quoted is
		// better than writing it raw and producing an unparseable dump.
		return quote(fmt.Sprint(t))
	}
}

// quote wraps a string in single quotes and escapes what a single-quoted MySQL
// string cannot hold literally.
//
// The loop walks bytes, not runes, and that is safe: UTF-8 continuation bytes
// are all above 0x7f and none of the cases below is, so a multi-byte character
// is copied through untouched.
func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case 0x00:
			b.WriteString(`\0`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case 0x1a:
			// Ctrl-Z ends the file for some Windows tools; MySQL spells it \Z.
			b.WriteString(`\Z`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// countingWriter counts the bytes handed to the underlying writer, so Result
// can report the size of the dump without buffering it to measure it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Filename is the name the dump is offered under: sortable by date and it says
// what it is without opening it.
func Filename(now time.Time) string {
	return "dvbs-backup-" + now.UTC().Format("20060102-150405") + ".sql"
}
