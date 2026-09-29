package config

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"

	"dvbs/internal/dataservice"
	"dvbs/internal/mailer"
)

type Config struct {
	DatabaseURL string
	Port        string
	JWTSecret   string
	AppOrigin   string
	UploadDir   string

	CookieSecure  bool
	SessionMaxAge time.Duration
	ResetTokenTTL time.Duration
	DBMaxConns    int32
	BcryptCost    int

	AdminEmail    string
	AdminPassword string

	SMTP mailer.SMTPConfig

	TrustedProxies []string

	AuthRatePerMinute   int
	AuthRateBurst       int
	UploadRatePerMinute int
	UploadRateBurst     int
	TIDBHost            string

	// DataService points at the TiDB Cloud Data App whose endpoints carry the
	// catalogue. Since the catalogue no longer has a SQL fallback, the
	// credential is not conditional: a server that cannot reach its own
	// endpoints cannot serve books, so Load() refuses to start with a missing
	// URL or key. It is a separate credential from DATABASE_URL.
	DataService dataservice.Config

	// BackupDir is where the backups interface keeps the logical dumps the
	// panel takes. The directory is created at start if it does not exist.
	BackupDir string
}

const (
	defaultPort                = "8000"
	defaultUploadDir           = "uploads"
	defaultBackupDir           = "backups"
	defaultSessionTTL          = 15 * time.Minute
	defaultResetTTL            = 30 * time.Minute
	defaultDBMaxConns          = 20
	defaultBcryptCost          = 12
	defaultSMTPPort            = 587
	defaultAuthRatePerMinute   = 10
	defaultAuthRateBurst       = 20
	defaultUploadRatePerMinute = 20
	defaultUploadRateBurst     = 10
	minSecretLengthLen         = 32
	maxResetTTLMinutes         = 24 * 60
)

const tidbTLSConfigName = "tidb"

func Load() (*Config, error) {
	var errs []error

	port := getEnv("PORT", defaultPort)
	uploadDir := getEnv("UPLOAD_DIR", defaultUploadDir)
	dbMaxConns, err := getEnvInt("DB_MAX_CONNS", defaultDBMaxConns)
	if err != nil {
		errs = append(errs, err)
	}
	bcryptCost, err := getEnvInt("BCRYPT_COST", defaultBcryptCost)
	if err != nil {
		errs = append(errs, err)
	}
	smtpPort, err := getEnvInt("SMTP_PORT", defaultSMTPPort)
	if err != nil {
		errs = append(errs, err)
	}
	sessionTTLMinutes, err := getEnvInt("SESSION_TTL_MINUTES", int(defaultSessionTTL/time.Minute))
	if err != nil {
		errs = append(errs, err)
	}
	resetTTLMinutes, err := getEnvInt("RESET_TOKEN_TTL_MINUTES", int(defaultResetTTL/time.Minute))
	if err != nil {
		errs = append(errs, err)
	}
	cookieSecure, err := getEnvBool("COOKIE_SECURE", false)
	if err != nil {
		errs = append(errs, err)
	}
	authRate, err := getEnvInt("AUTH_RATE_PER_MINUTE", defaultAuthRatePerMinute)
	if err != nil {
		errs = append(errs, err)
	}
	authBurst, err := getEnvInt("AUTH_RATE_BURST", defaultAuthRateBurst)
	if err != nil {
		errs = append(errs, err)
	}
	uploadRate, err := getEnvInt("UPLOAD_RATE_PER_MINUTE", defaultUploadRatePerMinute)
	if err != nil {
		errs = append(errs, err)
	}
	uploadBurst, err := getEnvInt("UPLOAD_RATE_BURST", defaultUploadRateBurst)
	if err != nil {
		errs = append(errs, err)
	}

	cfg := &Config{
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		Port:                port,
		AppOrigin:           os.Getenv("APP_ORIGIN"),
		UploadDir:           uploadDir,
		BackupDir:           getEnv("BACKUP_DIR", defaultBackupDir),
		AdminEmail:          os.Getenv("ADMIN_EMAIL"),
		AdminPassword:       os.Getenv("ADMIN_PASSWORD"),
		DBMaxConns:          int32(dbMaxConns),
		BcryptCost:          bcryptCost,
		ResetTokenTTL:       time.Duration(resetTTLMinutes) * time.Minute,
		TrustedProxies:      splitList(os.Getenv("TRUSTED_PROXIES")),
		AuthRatePerMinute:   authRate,
		AuthRateBurst:       authBurst,
		UploadRatePerMinute: uploadRate,
		UploadRateBurst:     uploadBurst,
		SMTP: mailer.SMTPConfig{
			Host:     os.Getenv("SMTP_HOST"),
			Port:     smtpPort,
			User:     os.Getenv("SMTP_USER"),
			Password: os.Getenv("SMTP_PASSWORD"),
			From:     os.Getenv("SMTP_FROM"),
		},
	}

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		errs = append(errs, errors.New("JWT_SECRET is required: generate one with `openssl rand -base64 48`"))
	} else if len(secret) < minSecretLengthLen {
		errs = append(errs, fmt.Errorf("JWT_SECRET must be at least %d characters, got %d", minSecretLengthLen, len(secret)))
	}
	cfg.JWTSecret = secret

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}

	if err := validatePort(cfg.Port); err != nil {
		errs = append(errs, err)
	}

	if cfg.AuthRatePerMinute <= 0 {
		errs = append(errs, errors.New("AUTH_RATE_PER_MINUTE must be greater than zero"))
	}
	if cfg.AuthRateBurst <= 0 {
		errs = append(errs, errors.New("AUTH_RATE_BURST must be greater than zero"))
	}
	if cfg.UploadRatePerMinute <= 0 {
		errs = append(errs, errors.New("UPLOAD_RATE_PER_MINUTE must be greater than zero"))
	}
	if cfg.UploadRateBurst <= 0 {
		errs = append(errs, errors.New("UPLOAD_RATE_BURST must be greater than zero"))
	}

	if cfg.AppOrigin != "" {
		origin, err := normalizeOrigin(cfg.AppOrigin)
		if err != nil {
			errs = append(errs, err)
		} else {
			cfg.AppOrigin = origin
		}
	}

	cfg.CookieSecure = cookieSecure
	if !cfg.CookieSecure && strings.EqualFold(os.Getenv("APP_ENV"), "production") {
		errs = append(errs, errors.New("COOKIE_SECURE must be true when APP_ENV=production"))
	}
	cfg.SessionMaxAge = time.Duration(sessionTTLMinutes) * time.Minute
	if cfg.SessionMaxAge <= 0 {
		errs = append(errs, fmt.Errorf("SESSION_TTL_MINUTES must be positive, got %d", sessionTTLMinutes))
	}

	if resetTTLMinutes <= 0 || resetTTLMinutes > maxResetTTLMinutes {
		errs = append(errs, fmt.Errorf("RESET_TOKEN_TTL_MINUTES must be between 1 and %d, got %d", maxResetTTLMinutes, resetTTLMinutes))
	}

	if cfg.DBMaxConns < 1 {
		errs = append(errs, fmt.Errorf("DB_MAX_CONNS must be at least 1, got %d", cfg.DBMaxConns))
	}

	if cfg.BcryptCost < 4 || cfg.BcryptCost > 31 {
		errs = append(errs, fmt.Errorf("BCRYPT_COST must be between 4 and 31, got %d", cfg.BcryptCost))
	}

	if (cfg.AdminEmail == "") != (cfg.AdminPassword == "") {
		errs = append(errs, errors.New("ADMIN_EMAIL and ADMIN_PASSWORD must be set together"))
	}
	if cfg.AdminEmail != "" && !strings.Contains(cfg.AdminEmail, "@") {
		errs = append(errs, fmt.Errorf("ADMIN_EMAIL %q is not a valid email", cfg.AdminEmail))
	}

	cfg.TIDBHost = os.Getenv("TIDB_HOST")
	if cfg.TIDBHost == "" {
		if u, err := url.Parse(cfg.DatabaseURL); err == nil {
			cfg.TIDBHost = u.Hostname()
		}
	}

	cfg.DataService = dataservice.Config{
		BaseURL:    os.Getenv("TIDB_DS_BASE_URL"),
		PublicKey:  os.Getenv("TIDB_DS_PUBLIC_KEY"),
		PrivateKey: os.Getenv("TIDB_DS_PRIVATE_KEY"),
	}

	// The catalogue has no SQL fallback anymore, which makes the Data App
	// credential load-bearing: a server that cannot reach its own book
	// endpoints cannot serve its store. All-or-nothing here, or a
	// half-configured key surfaces as a 401 from an endpoint on the first admin
	// click instead of failing startup.
	if err := cfg.DataService.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("TIDB_DS_*: %w", err))
	}

	return cfg, errors.Join(errs...)
}

func NewDBPool(cfg *Config) (*sql.DB, error) {
	dsn := ParseDSN(cfg.DatabaseURL)
	if !strings.Contains(dsn, "tls=") {
		dsn += "&tls=" + tidbTLSConfigName
	}
	if err := RegisterTLSConfig(cfg.TIDBHost); err != nil {
		return nil, fmt.Errorf("TLS config: %w", err)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Pool settings per AGENTS.md §3.6: TiDB Cloud Starter closes idle at 340s.
	// ConnMaxLifetime must be < 340s; 5 min is safe.
	db.SetMaxOpenConns(int(cfg.DBMaxConns))
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

func RegisterTLSConfig(host string) error {
	return mysql.RegisterTLSConfig(tidbTLSConfigName, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         host,
		InsecureSkipVerify: false,
	})
}

// parseDSN converts a mysql:// URL to go-sql-driver DSN format.
// Accepts: mysql://user:pass@host:port/db?params
// Returns: user:pass@tcp(host:port)/db?params
func ParseDSN(urlStr string) string {
	// If already in DSN format (no scheme), return as-is
	if !strings.HasPrefix(urlStr, "mysql://") {
		return urlStr
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		// If parsing fails, return original
		return urlStr
	}
	host := u.Host
	if u.Port() != "" {
		host = u.Host
	}
	// Remove leading / from path
	dbname := strings.TrimPrefix(u.Path, "/")
	dsn := u.User.String() + "@tcp(" + host + ")/" + dbname
	if u.RawQuery != "" {
		dsn += "?" + u.RawQuery
	}
	return dsn
}

func validatePort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("PORT must be an integer between 1 and 65535, got %q", port)
	}
	return nil
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("APP_ORIGIN is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("APP_ORIGIN must use http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("APP_ORIGIN must include a host, got %q", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("APP_ORIGIN must not include a path, got %q", u.Path)
	}
	return u.Scheme + "://" + u.Host, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	return v, nil
}

func getEnvBool(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (true/false/1/0), got %q", key, raw)
	}
	return v, nil
}
