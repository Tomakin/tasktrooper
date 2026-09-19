package runtime

import (
	"os"

	"github.com/rs/zerolog/log"
)

// processSecretVars are the credentials the desktop app passes to this process
// as plaintext environment variables. They are read exactly once, at boot, by
// cmd/agent-server/env.go and by the secrets cipher; after that the process
// holds the values it needs in memory and the environment entries are pure
// liability.
//
// They are dropped here rather than left in place because scrubbing the *child*
// environment (internal/platform/childenv) does not remove them from the
// *parent*, and exec sites outside the three that were hardened still build
// their child environment from os.Environ() — internal/adapter/git among them.
// Anything an agent can reach through those inherits whatever is still set
// here. Unsetting closes all of them at once, and closes them for exec sites
// added in future that forget to scrub.
//
// Read the doc comment on scrubProcessSecrets before adding a name: unsetting a
// variable something re-reads later breaks the process, silently, at the moment
// the re-read happens rather than at boot.
var processSecretVars = []string{
	// Postgres DSN, password included. Consumed by optionsFromEnv →
	// Options.PostgresDSN → cfg.Storage.Postgres.DSN and by the pool
	// cmd/agent-server opens before runtime.Run. config.yml expands
	// ${POSTGRES_DSN}, a different name, so a config reload does not need it.
	"DATABASE_URL",
	// The bearer token every request must carry. Consumed by optionsFromEnv →
	// Options.APIKey; config.yml expands ${SERVER_API_KEY} but a reload
	// re-applies the Options value over it (applyLocalOverrides), so nothing
	// re-reads the variable.
	"SERVER_API_KEY",
	// Encrypts provider API keys, MCP secrets and store credentials at rest.
	// secrets.NewCipherFromEnv reads it, including on a config reload, so the
	// engine derives the cipher once before this runs and hands the derived
	// cipher to every later caller (see engine.initSecretsCipher).
	"MCP_SECRETS_KEY",
	// bcrypt hashes of the web sign-in passwords. Consumed by optionsFromEnv →
	// Options.WebAuthUsers; nothing re-reads it. An agent that could read the
	// hashes could try to crack them offline.
	"WEB_AUTH_USERS",
}

// scrubProcessSecrets removes the pod's injected credentials from the process
// environment once their values are held in memory.
//
// IMPORTANT — what this does not do. On Linux /proc/<pid>/environ is served
// from the [env_start, env_end) region of the initial process stack, fixed at
// execve. os.Unsetenv edits the runtime's own copy of the environment; it does
// not touch that region, and there is no unprivileged way to rewrite it
// (PR_SET_MM_ENV_START needs CAP_SYS_RESOURCE). So a child that runs
// `cat /proc/$PPID/environ` still reads every value that was set at exec.
// That door is closed separately, by denyProcEnvironReads.
//
// What this does close is every exec site that builds its child environment
// from os.Environ() — including the ones outside the hardened three — plus
// ${VAR} expansion of user-supplied MCP server configs
// (internal/application/mcp/resolve.go), which would otherwise let a config
// simply ask for ${DATABASE_URL} by name.
//
// Failures are logged and ignored: os.Unsetenv only fails on a malformed name,
// and refusing to boot over it would trade a leak for an outage.
func scrubProcessSecrets() {
	for _, name := range processSecretVars {
		if _, present := os.LookupEnv(name); !present {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			log.Warn().Err(err).Str("var", name).Msg("could not unset secret from process environment")
			continue
		}
		log.Debug().Str("var", name).Msg("secret removed from process environment")
	}
}
