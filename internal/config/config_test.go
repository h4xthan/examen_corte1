package config

import (
	"strings"
	"testing"
	"time"
)

// everyVar is every variable Load reads.
//
// The tests used to set only the two they cared about and inherit the rest from
// the shell, which meant a developer who had loaded .env, or exported anything
// by hand, saw failures that had nothing to do with the code under test:
// DB_MAX_CONNS from .env broke the default assertions, and a lone ADMIN_EMAIL
// broke the pairing test. Clearing all of them first makes a config test
// describe Load's defaults and nothing else. t.Setenv restores the previous
// value on cleanup, so this does not damage the caller's environment.
var everyVar = []string{
	"ADMIN_EMAIL", "ADMIN_PASSWORD", "APP_ENV", "APP_ORIGIN", "AUTH_RATE_BURST",
	"AUTH_RATE_PER_MINUTE", "BACKUP_DIR", "BCRYPT_COST", "COOKIE_SECURE",
	"DATABASE_URL", "DB_MAX_CONNS", "JWT_SECRET", "PORT", "RESET_TOKEN_TTL_MINUTES",
	"SESSION_TTL_MINUTES", "SMTP_FROM", "SMTP_HOST", "SMTP_PASSWORD", "SMTP_PORT",
	"SMTP_USER", "TIDB_DS_BASE_URL", "TIDB_DS_PRIVATE_KEY", "TIDB_DS_PUBLIC_KEY",
	"TIDB_HOST", "TRUSTED_PROXIES", "UPLOAD_DIR", "UPLOAD_RATE_BURST",
	"UPLOAD_RATE_PER_MINUTE",
}

// setEnv clears everything Load reads, then sets the variables a test needs.
// An empty value is how every helper in this file spells "unset", so clearing
// and defaulting are the same thing.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range everyVar {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// validEnv is the minimum that satisfies Load, so each test can override one
// variable at a time and assert on the specific failure it is about.
//
// The Data App credential is part of that minimum now. The catalogue has no
// SQL fallback, so a config that satisfies Load is one that can reach its own
// book endpoints, and every other test builds on that base.
func validEnv(t *testing.T) map[string]string {
	return map[string]string{
		"DATABASE_URL":        "mysql://dvbs:dvbs@localhost:4000/dvbs",
		"JWT_SECRET":          strings.Repeat("s", 40),
		"TIDB_DS_BASE_URL":    "https://us-east-1.data.tidbcloud.com/api/v1beta/app/dataapp-x/endpoint",
		"TIDB_DS_PUBLIC_KEY":  "pub",
		"TIDB_DS_PRIVATE_KEY": "priv",
	}
}

func TestLoadAcceptsMinimalValidConfig(t *testing.T) {
	setEnv(t, validEnv(t))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Port = %q, want %q", cfg.Port, defaultPort)
	}
	if cfg.DBMaxConns != defaultDBMaxConns {
		t.Errorf("DBMaxConns = %d, want %d", cfg.DBMaxConns, defaultDBMaxConns)
	}
	if cfg.BcryptCost != defaultBcryptCost {
		t.Errorf("BcryptCost = %d, want %d", cfg.BcryptCost, defaultBcryptCost)
	}
	if cfg.SessionMaxAge != defaultSessionTTL {
		t.Errorf("SessionMaxAge = %v, want %v", cfg.SessionMaxAge, defaultSessionTTL)
	}
}

// TestLoadRequiresJWTSecret is the regression for the original hardcoded
// secret: there must be no fallback that lets the server boot without one.
func TestLoadRequiresJWTSecret(t *testing.T) {
	env := validEnv(t)
	delete(env, "JWT_SECRET")
	setEnv(t, env)
	t.Setenv("JWT_SECRET", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() succeeded without JWT_SECRET; the server would boot with a guessable signing key")
	} else if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("error = %v, want it to name JWT_SECRET", err)
	}
}

func TestLoadRejectsShortJWTSecret(t *testing.T) {
	env := validEnv(t)
	env["JWT_SECRET"] = "short"
	setEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a JWT_SECRET under the minimum length")
	}
}

// TestLoadRejectsMalformedNumbers covers the silent-fallback regression: a typo
// in a numeric setting must be reported, not quietly replaced by the default.
func TestLoadRejectsMalformedNumbers(t *testing.T) {
	for _, key := range []string{"DB_MAX_CONNS", "BCRYPT_COST", "SMTP_PORT", "SESSION_TTL_MINUTES"} {
		t.Run(key, func(t *testing.T) {
			env := validEnv(t)
			env[key] = "not-a-number"
			setEnv(t, env)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted %s=not-a-number", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("error = %v, want it to name %s", err, key)
			}
		})
	}
}

func TestLoadRejectsMalformedBoolean(t *testing.T) {
	env := validEnv(t)
	env["COOKIE_SECURE"] = "maybe"
	setEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted COOKIE_SECURE=maybe and defaulted it to false")
	}
}

// TestLoadRejectsInsecureCookiesInProduction guards the deployment footgun:
// plain-http session cookies are only acceptable outside production.
func TestLoadRejectsInsecureCookiesInProduction(t *testing.T) {
	env := validEnv(t)
	env["APP_ENV"] = "production"
	env["COOKIE_SECURE"] = "false"
	setEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load() allowed COOKIE_SECURE=false with APP_ENV=production")
	}
}

func TestLoadRejectsOutOfRangeBcryptCost(t *testing.T) {
	for _, cost := range []string{"3", "32"} {
		t.Run(cost, func(t *testing.T) {
			env := validEnv(t)
			env["BCRYPT_COST"] = cost
			setEnv(t, env)

			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted BCRYPT_COST=%s, which panics inside bcrypt", cost)
			}
		})
	}
}

func TestLoadRequiresAdminCredentialsTogether(t *testing.T) {
	env := validEnv(t)
	env["ADMIN_EMAIL"] = "admin@dvbs.test"
	setEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted ADMIN_EMAIL without ADMIN_PASSWORD")
	}
}

// TestLoadNormalizesOrigin keeps the CORS comparison in middleware a plain
// string equality check, so a trailing slash cannot silently stop matching.
func TestLoadNormalizesOrigin(t *testing.T) {
	env := validEnv(t)
	env["APP_ORIGIN"] = "https://dvbs.test/"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AppOrigin != "https://dvbs.test" {
		t.Fatalf("AppOrigin = %q, want %q", cfg.AppOrigin, "https://dvbs.test")
	}
}

func TestLoadRejectsOriginWithPath(t *testing.T) {
	env := validEnv(t)
	env["APP_ORIGIN"] = "https://dvbs.test/admin"
	setEnv(t, env)

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an APP_ORIGIN with a path")
	}
}

// TestLoadReportsEveryProblemAtOnce means a misconfigured deployment gets the
// full list on the first boot instead of one error per restart.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "",
		"JWT_SECRET":   "short",
		"PORT":         "not-a-port",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded with an empty DATABASE_URL")
	}
	for _, want := range []string{"DATABASE_URL", "JWT_SECRET", "PORT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestLoadSessionMaxAgeFromMinutes(t *testing.T) {
	env := validEnv(t)
	env["SESSION_TTL_MINUTES"] = "45"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SessionMaxAge != 45*time.Minute {
		t.Fatalf("SessionMaxAge = %v, want 45m", cfg.SessionMaxAge)
	}
}

func TestSMTPFallsBackToLogBackendWhenUnset(t *testing.T) {
	env := validEnv(t)
	env["SMTP_HOST"] = ""
	env["SMTP_FROM"] = ""
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SMTP.Host != "" || cfg.SMTP.From != "" {
		t.Fatal("expected an empty SMTP block so the Log backend is selected")
	}
	// The port still defaults, which must not by itself look like SMTP enabled.
	if cfg.SMTP.Port != defaultSMTPPort {
		t.Fatalf("SMTP.Port = %d, want the default %d", cfg.SMTP.Port, defaultSMTPPort)
	}
}

// The Data App credential became load-bearing when the catalogue lost its SQL
// fallback: a server with the pool up but no endpoints cannot serve its shop.
// A missing URL or key has to stop startup, or the first admin click surfaces a
// 401 from an endpoint nobody configured.
func TestLoadRequiresTheDataServiceCredential(t *testing.T) {
	setEnv(t, validEnv(t))
	t.Setenv("TIDB_DS_BASE_URL", "")
	t.Setenv("TIDB_DS_PUBLIC_KEY", "")
	t.Setenv("TIDB_DS_PRIVATE_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("want an error: the catalogue is served from this credential and it is missing")
	}
	if !strings.Contains(err.Error(), "TIDB_DS_BASE_URL") {
		t.Errorf("the message should name what is missing, got %v", err)
	}
}

func TestLoadRejectsADataServiceKeyOverPlainHTTP(t *testing.T) {
	setEnv(t, validEnv(t))
	t.Setenv("TIDB_DS_BASE_URL", "http://data.example.test/api")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("want a complaint about the scheme, got %v", err)
	}
}

func TestLoadAcceptsACompleteDataServiceConfiguration(t *testing.T) {
	setEnv(t, validEnv(t))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.DataService.BaseURL == "" || cfg.DataService.PrivateKey == "" {
		t.Errorf("the credential did not reach the config: %+v", cfg.DataService)
	}
}

func TestBackupDirDefaultsToTheBackupsDirectory(t *testing.T) {
	setEnv(t, validEnv(t))
	t.Setenv("BACKUP_DIR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.BackupDir != defaultBackupDir {
		t.Errorf("BackupDir = %q, want %q", cfg.BackupDir, defaultBackupDir)
	}
}

func TestBackupDirIsConfigurable(t *testing.T) {
	setEnv(t, validEnv(t))
	t.Setenv("BACKUP_DIR", "/var/lib/dvbs/backups")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.BackupDir != "/var/lib/dvbs/backups" {
		t.Errorf("BackupDir = %q", cfg.BackupDir)
	}
}
