package store

import (
	"database/sql"
	"os"
	"testing"

	"dvbs/internal/config"
	"dvbs/internal/store/db"
	_ "github.com/go-sql-driver/mysql"
)

var (
	testQueries *db.Queries
	testDB      *sql.DB
)

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
	testQueries = db.New(pool)
	os.Exit(m.Run())
}
