package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"sort"
	"strings"

	"dvbs/internal/config"
	_ "github.com/go-sql-driver/mysql"
)

//go:embed migrations/*.up.sql
var migrationFiles embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := config.RegisterTLSConfig(cfg.TIDBHost); err != nil {
		log.Fatalf("TLS config: %v", err)
	}

	dsn := config.ParseDSN(cfg.DatabaseURL)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}
	fmt.Println("Ping successful!")

	// Find migration files from embedded FS
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		log.Fatalf("read migrations: %v", err)
	}

	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	if len(files) == 0 {
		log.Fatal("no migration files found")
	}

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Fatalf("begin tx: %v", err)
	}

	for _, f := range files {
		path := "migrations/" + f
		content, err := migrationFiles.ReadFile(path)
		if err != nil {
			tx.Rollback()
			log.Fatalf("read %s: %v", f, err)
		}

		stmts := splitStatements(string(content))
		for _, stmt := range stmts {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" || strings.HasPrefix(stmt, "--") {
				continue
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				// Ignore duplicate key/constraint errors (idempotency)
				if strings.Contains(err.Error(), "Error 1061") || // Duplicate key name (index)
					strings.Contains(err.Error(), "Error 1826") || // Duplicate foreign key constraint name
					strings.Contains(err.Error(), "Error 1091") || // Can't DROP foreign key (unknown)
					strings.Contains(err.Error(), "Error 1050") || // Table already exists
					strings.Contains(err.Error(), "Error 1060") || // Duplicate column name
					strings.Contains(err.Error(), "Duplicate entry") {
					log.Printf("skip (already exists): %s", stmt[:min(100, len(stmt))])
					continue
				}
				tx.Rollback()
				log.Fatalf("exec %s: %v\nstatement: %s", f, err, stmt[:min(200, len(stmt))])
			}
		}
		log.Printf("applied %s", f)
	}

	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}
	log.Println("migrations applied successfully")
}

func splitStatements(sql string) []string {
	// Remove comment lines first
	lines := strings.Split(sql, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		// Remove inline comments
		if idx := strings.Index(trimmed, "--"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
			if trimmed == "" {
				continue
			}
		}
		cleaned = append(cleaned, trimmed)
	}
	sql = strings.Join(cleaned, " ")

	// Now split on semicolons
	var out []string
	var buf strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	escaped := false

	for _, r := range sql {
		buf.WriteRune(r)

		if escaped {
			escaped = false
			continue
		}

		switch r {
		case '\\':
			escaped = true
		case '\'':
			if !inDoubleQuote && !inBacktick {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote && !inBacktick {
				inDoubleQuote = !inDoubleQuote
			}
		case '`':
			if !inSingleQuote && !inDoubleQuote {
				inBacktick = !inBacktick
			}
		case ';':
			if !inSingleQuote && !inDoubleQuote && !inBacktick {
				out = append(out, buf.String())
				buf.Reset()
			}
		}
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	// Filter out empty statements
	var filtered []string
	for _, stmt := range out {
		stmt = strings.TrimSpace(stmt)
		if stmt != "" {
			filtered = append(filtered, stmt)
		}
	}
	return filtered
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
