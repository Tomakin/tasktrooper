package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/makifbaysal/tasktrooper/server/internal/application/webauth"
	"github.com/makifbaysal/tasktrooper/server/internal/platform/runtime"
)

// defaultShutdownGrace is how long the process keeps working after SIGTERM
// before in-flight agent runs are cancelled. The desktop supervisor gives up
// sooner than this, so the number that governs a quit is there; what this one
// buys is the terminal case (Ctrl-C in `make dev`), where a cancelled run still
// gets to write its terminal status.
const defaultShutdownGrace = 9 * time.Minute

const (
	defaultPort    = 8085
	defaultDataDir = "./data"
)

// shutdownGraceFromEnv reads SHUTDOWN_GRACE (a Go duration, e.g. "5m").
// Unset or unparseable falls back to the default rather than failing boot — a
// typo in an env var must not keep the server from starting.
func shutdownGraceFromEnv(getenv func(string) string) time.Duration {
	raw := getenv("SHUTDOWN_GRACE")
	if raw == "" {
		return defaultShutdownGrace
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultShutdownGrace
	}
	return d
}

// localConfig is everything this process reads out of the environment. The
// fields beside Options are the ones consumed before the runtime exists: a
// database has to be running before migrations, and the embedded cluster is
// what makes that true when DATABASE_URL is empty.
type localConfig struct {
	Options runtime.Options
	// PostgresDSN empty means: start the embedded cluster under DataDir.
	PostgresDSN    string
	PostgresBinDir string
	ShutdownGrace  time.Duration
}

// optionsFromEnv reads the server's configuration out of the environment. It is
// the last place DATABASE_URL, SERVER_API_KEY and MCP_SECRETS_KEY are needed:
// everything downstream takes them from the returned config, and runtime.Run
// drops them from the process environment once it has them (see
// internal/platform/runtime/envscrub.go) so an agent's child process cannot
// reach them.
//
// getenv is a parameter rather than os.Getenv so tests can supply an
// environment; keep it side-effect free for that reason.
func optionsFromEnv(getenv func(string) string) (localConfig, error) {
	apiKey := strings.TrimSpace(getenv("SERVER_API_KEY"))
	if apiKey == "" {
		return localConfig{}, errors.New("SERVER_API_KEY is required (the bearer token the UI sends)")
	}
	if getenv("MCP_SECRETS_KEY") == "" {
		return localConfig{}, errors.New("MCP_SECRETS_KEY is required (encrypts provider API keys at rest)")
	}

	dataDir := getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	configPath := getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "resources/config.yml"
	}

	// 0 is a real value here and not "unset": it asks the kernel for a free
	// port, which is how the desktop starts a server without colliding with
	// whatever else the user is running. The port it got goes to stdout.
	port := defaultPort
	if v := getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 0 {
			return localConfig{}, fmt.Errorf("invalid PORT %q", v)
		}
		port = p
	}

	webUsers, err := webAuthUsersFromEnv(getenv, os.ReadFile)
	if err != nil {
		return localConfig{}, err
	}

	listenHost, err := listenHostFromEnv(getenv("LISTEN_HOST"), len(webUsers) > 0)
	if err != nil {
		return localConfig{}, err
	}

	dsn := strings.TrimSpace(getenv("DATABASE_URL"))
	return localConfig{
		Options: runtime.Options{
			ConfigPath:  configPath,
			DataDir:     dataDir,
			Port:        port,
			APIKey:      apiKey,
			PostgresDSN: dsn,
			// Connect MCP servers after the listener is up: a slow or hanging
			// stdio server would otherwise hold the desktop on its splash
			// screen for as long as it takes to time out.
			LazyMCP:           true,
			CORSOrigins:       corsOriginsFromEnv(getenv("CORS_ORIGINS")),
			EmbeddingsBaseURL: strings.TrimSpace(getenv("EMBEDDINGS_BASE_URL")),
			AllowedRoots:      allowedRootsFromEnv(getenv("ALLOWED_ROOTS")),
			UIRoot:            strings.TrimSpace(getenv("WEB_UI_DIR")),
			WebAuthUsers:      webUsers,
			ListenHost:        listenHost,
			WebCookieInsecure: strings.TrimSpace(getenv("WEB_COOKIE_INSECURE")) == "1",
		},
		PostgresDSN:    dsn,
		PostgresBinDir: strings.TrimSpace(getenv("EMBEDDED_POSTGRES_CACHE_DIR")),
		ShutdownGrace:  shutdownGraceFromEnv(getenv),
	}, nil
}

// allowedRootsFromEnv splits on the OS path-list separator, like PATH, because
// a comma is a legal character in a directory name.
func allowedRootsFromEnv(raw string) []string {
	var out []string
	for _, part := range filepath.SplitList(raw) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func corsOriginsFromEnv(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return runtime.DefaultCORSOrigins
	}
	return out
}

// webAuthUsersFromEnv reads WEB_AUTH_USERS and WEB_AUTH_USERS_FILE; both may be
// set and are merged. The file form exists because a bcrypt hash is full of `$`,
// which an env file sourced by a shell would expand. A malformed entry fails
// boot: a sign-in that silently lost a user is worse than a server that says
// why it did not start.
func webAuthUsersFromEnv(getenv func(string) string, readFile func(string) ([]byte, error)) ([]webauth.User, error) {
	raw := getenv("WEB_AUTH_USERS")
	if path := strings.TrimSpace(getenv("WEB_AUTH_USERS_FILE")); path != "" {
		b, err := readFile(path)
		if err != nil {
			return nil, fmt.Errorf("WEB_AUTH_USERS_FILE: %w", err)
		}
		raw += "\n" + string(b)
	}
	users, err := webauth.ParseUsers(raw)
	if err != nil {
		return nil, fmt.Errorf("WEB_AUTH_USERS: %w", err)
	}
	return users, nil
}

// listenHostFromEnv reads LISTEN_HOST. Anything beyond loopback puts the UI, the
// API and through them agents with a terminal on the network, so it is refused
// unless browser sign-in is configured: the bearer token alone was never meant
// to guard a port other machines can reach.
func listenHostFromEnv(raw string, webAuth bool) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" || host == "127.0.0.1" {
		return "", nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("LISTEN_HOST %q is not an IP address", host)
	}
	if ip.IsLoopback() {
		return ip.String(), nil
	}
	if !webAuth {
		return "", errors.New("LISTEN_HOST exposes the server to the network; set WEB_AUTH_USERS or WEB_AUTH_USERS_FILE so it asks for a sign-in")
	}
	return ip.String(), nil
}
