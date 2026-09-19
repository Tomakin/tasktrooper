package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/makifbaysal/tasktrooper/server/internal/application/webauth"
	"github.com/makifbaysal/tasktrooper/server/internal/platform/runtime"
)

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		"SERVER_API_KEY":  "local-dev-key",
		"MCP_SECRETS_KEY": "c2VjcmV0LWtleQ==",
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Run("happy path with defaults", func(t *testing.T) {
		cfg, err := optionsFromEnv(fakeEnv(validEnv()))
		if err != nil {
			t.Fatal(err)
		}
		// An empty DSN is a complete configuration: it means the embedded
		// cluster, which is what a first run has.
		if cfg.PostgresDSN != "" {
			t.Fatalf("dsn = %q, want empty", cfg.PostgresDSN)
		}
		if cfg.Options.APIKey != "local-dev-key" {
			t.Fatalf("api key = %q", cfg.Options.APIKey)
		}
		if cfg.Options.Port != defaultPort {
			t.Fatalf("port = %d, want %d", cfg.Options.Port, defaultPort)
		}
		if cfg.Options.DataDir != defaultDataDir {
			t.Fatalf("data dir = %q, want %q", cfg.Options.DataDir, defaultDataDir)
		}
		if cfg.Options.ConfigPath != "resources/config.yml" {
			t.Fatalf("config path = %q", cfg.Options.ConfigPath)
		}
		if len(cfg.Options.CORSOrigins) != len(runtime.DefaultCORSOrigins) {
			t.Fatalf("cors origins = %v", cfg.Options.CORSOrigins)
		}
	})

	t.Run("external database", func(t *testing.T) {
		env := validEnv()
		env["DATABASE_URL"] = "postgres://u:p@10.0.0.5:5432/tasktrooper"
		cfg, err := optionsFromEnv(fakeEnv(env))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.PostgresDSN != env["DATABASE_URL"] || cfg.Options.PostgresDSN != env["DATABASE_URL"] {
			t.Fatalf("cfg = %+v", cfg)
		}
	})

	t.Run("custom port and dirs", func(t *testing.T) {
		env := validEnv()
		env["PORT"] = "9090"
		env["DATA_DIR"] = "/mnt/tt"
		env["CONFIG_PATH"] = "/etc/tt/config.yml"
		env["EMBEDDED_POSTGRES_CACHE_DIR"] = "/mnt/tt/pgbin"
		cfg, err := optionsFromEnv(fakeEnv(env))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Options.Port != 9090 || cfg.Options.DataDir != "/mnt/tt" ||
			cfg.Options.ConfigPath != "/etc/tt/config.yml" || cfg.PostgresBinDir != "/mnt/tt/pgbin" {
			t.Fatalf("cfg = %+v", cfg)
		}
	})

	// PORT=0 is how the desktop avoids colliding with whatever else is running:
	// it must survive as 0 rather than fall back to the default.
	t.Run("port zero means pick one", func(t *testing.T) {
		env := validEnv()
		env["PORT"] = "0"
		cfg, err := optionsFromEnv(fakeEnv(env))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Options.Port != 0 {
			t.Fatalf("port = %d, want 0", cfg.Options.Port)
		}
	})

	t.Run("explicit cors origins", func(t *testing.T) {
		env := validEnv()
		env["CORS_ORIGINS"] = "https://a.example , https://b.example"
		cfg, err := optionsFromEnv(fakeEnv(env))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"https://a.example", "https://b.example"}
		if len(cfg.Options.CORSOrigins) != 2 || cfg.Options.CORSOrigins[0] != want[0] || cfg.Options.CORSOrigins[1] != want[1] {
			t.Fatalf("cors = %v", cfg.Options.CORSOrigins)
		}
	})

	t.Run("allowed roots split like PATH", func(t *testing.T) {
		env := validEnv()
		sep := string(filepath.ListSeparator)
		env["ALLOWED_ROOTS"] = "/home/me/code, with comma" + sep + sep + " /srv/repos "
		cfg, err := optionsFromEnv(fakeEnv(env))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"/home/me/code, with comma", "/srv/repos"}
		if len(cfg.Options.AllowedRoots) != 2 || cfg.Options.AllowedRoots[0] != want[0] || cfg.Options.AllowedRoots[1] != want[1] {
			t.Fatalf("allowed roots = %q, want %q", cfg.Options.AllowedRoots, want)
		}
	})

	t.Run("no allowed roots by default", func(t *testing.T) {
		cfg, err := optionsFromEnv(fakeEnv(validEnv()))
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Options.AllowedRoots) != 0 {
			t.Fatalf("allowed roots = %q, want none", cfg.Options.AllowedRoots)
		}
	})

	for _, required := range []string{"SERVER_API_KEY", "MCP_SECRETS_KEY"} {
		t.Run("missing "+required, func(t *testing.T) {
			env := validEnv()
			delete(env, required)
			_, err := optionsFromEnv(fakeEnv(env))
			if err == nil || !strings.Contains(err.Error(), required) {
				t.Fatalf("err = %v, want mention of %s", err, required)
			}
		})
	}

	t.Run("invalid port", func(t *testing.T) {
		env := validEnv()
		env["PORT"] = "abc"
		if _, err := optionsFromEnv(fakeEnv(env)); err == nil {
			t.Fatal("want error for invalid PORT")
		}
	})
}

func TestShutdownGraceFromEnv(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"unset falls back", "", defaultShutdownGrace},
		{"explicit duration", "3m", 3 * time.Minute},
		// A typo must not keep the server from booting.
		{"garbage falls back", "banana", defaultShutdownGrace},
		{"non-positive falls back", "0s", defaultShutdownGrace},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shutdownGraceFromEnv(func(string) string { return tc.raw })
			if got != tc.want {
				t.Fatalf("shutdownGraceFromEnv(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWebAuthUsersFromEnv(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), webauth.MinBcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	noFile := func(string) ([]byte, error) { t.Fatal("no file configured"); return nil, nil }

	t.Run("unset means web sign-in is off", func(t *testing.T) {
		cfg, err := optionsFromEnv(fakeEnv(validEnv()))
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Options.WebAuthUsers) != 0 || cfg.Options.UIRoot != "" {
			t.Fatalf("users=%v uiRoot=%q", cfg.Options.WebAuthUsers, cfg.Options.UIRoot)
		}
	})

	t.Run("inline and file entries merge", func(t *testing.T) {
		env := map[string]string{
			"WEB_AUTH_USERS":      "alice:" + string(hash),
			"WEB_AUTH_USERS_FILE": "/etc/users",
		}
		users, err := webAuthUsersFromEnv(fakeEnv(env), func(p string) ([]byte, error) {
			if p != "/etc/users" {
				t.Fatalf("read %q", p)
			}
			return []byte("# web users\nbob:" + string(hash) + "\n"), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 2 || users[0].Name != "alice" || users[1].Name != "bob" {
			t.Fatalf("users = %+v", users)
		}
	})

	t.Run("a malformed entry fails boot", func(t *testing.T) {
		if _, err := webAuthUsersFromEnv(fakeEnv(map[string]string{"WEB_AUTH_USERS": "alice:plaintext"}), noFile); err == nil {
			t.Fatal("expected an error")
		}
		env := validEnv()
		env["WEB_AUTH_USERS"] = "alice"
		if _, err := optionsFromEnv(fakeEnv(env)); err == nil {
			t.Fatal("optionsFromEnv should refuse it too")
		}
	})

	t.Run("an unreadable file fails boot", func(t *testing.T) {
		_, err := webAuthUsersFromEnv(fakeEnv(map[string]string{"WEB_AUTH_USERS_FILE": "/missing"}),
			func(string) ([]byte, error) { return nil, os.ErrNotExist })
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("WEB_UI_DIR sets the UI root", func(t *testing.T) {
		env := validEnv()
		env["WEB_UI_DIR"] = " /srv/ui/dist "
		cfg, err := optionsFromEnv(fakeEnv(env))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Options.UIRoot != "/srv/ui/dist" {
			t.Fatalf("uiRoot = %q", cfg.Options.UIRoot)
		}
	})
}
