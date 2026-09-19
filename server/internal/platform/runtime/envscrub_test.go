package runtime

import (
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
)

// EnvScrubSuite covers the parent side of the leak.
//
// Scrubbing what a child inherits does nothing about the bridge process's own
// environment, and exec sites outside the hardened three still build
// their child environment from os.Environ() (internal/adapter/git among them).
// Removing the
// values from the process once they are held in memory closes all of those at
// once — and closes the exec site somebody adds next year without reading any
// of this.
type EnvScrubSuite struct {
	suite.Suite
}

func TestEnvScrubSuite(t *testing.T) {
	suite.Run(t, new(EnvScrubSuite))
}

func (s *EnvScrubSuite) TestInjectedSecretsAreRemovedFromTheProcess() {
	// The exact values the desktop app passes to this process.
	s.T().Setenv("DATABASE_URL", "postgres://tenant:hunter2@10.0.0.5:5432/tenant_x")
	s.T().Setenv("SERVER_API_KEY", "bearer-token-9f21")
	s.T().Setenv("MCP_SECRETS_KEY", "bWNwLXNlY3JldHMta2V5")

	scrubProcessSecrets()

	for _, name := range []string{"DATABASE_URL", "SERVER_API_KEY", "MCP_SECRETS_KEY"} {
		_, present := os.LookupEnv(name)
		s.False(present, "%s must not survive the scrub", name)
	}
	// os.Environ() is what every un-hardened exec site in the tree copies from.
	for _, entry := range os.Environ() {
		s.NotContains(entry, "hunter2")
		s.NotContains(entry, "bearer-token-9f21")
		s.NotContains(entry, "bWNwLXNlY3JldHMta2V5")
	}
}

// The scrub runs on every boot, including the desktop one where none of these
// are set. Unsetting an absent variable must be a no-op, not a warning storm.
func (s *EnvScrubSuite) TestAbsentSecretsAreNotAnError() {
	for _, name := range processSecretVars {
		s.T().Setenv(name, "placeholder")
		s.Require().NoError(os.Unsetenv(name))
	}

	s.NotPanics(scrubProcessSecrets)
}

// The blast radius of this list is the whole product: unsetting something that
// is read again later breaks production at the moment of the re-read, not at
// boot. Each name here was traced to its last reader before being added, and
// this test pins the ones that are deliberately NOT scrubbed so a future
// addition has to justify itself.
func (s *EnvScrubSuite) TestScrubListStaysMinimal() {
	s.ElementsMatch(
		[]string{"DATABASE_URL", "SERVER_API_KEY", "MCP_SECRETS_KEY", "WEB_AUTH_USERS"},
		processSecretVars,
	)

	// Left in place on purpose:
	//   POSTGRES_DSN    — resources/config.yml expands ${POSTGRES_DSN} again on
	//                     every config reload. DATABASE_URL is a different name
	//                     and is the one this process reads.
	//   ANTHROPIC/OPENAI/GOOGLE_API_KEY — llmprovider.BootstrapFromEnv reads them
	//                     at boot, and user MCP configs may legitimately reference
	//                     them by name through ${VAR} expansion on reload.
	for _, name := range processSecretVars {
		s.NotEqual("POSTGRES_DSN", name)
		s.NotEqual("ANTHROPIC_API_KEY", name)
		s.NotEqual("OPENAI_API_KEY", name)
		s.NotEqual("GOOGLE_API_KEY", name)
	}
}

// Non-secret configuration shares the environment with the secrets and must
// come through untouched: PORT, DATA_DIR and CONFIG_PATH are read by
// optionsFromEnv, SHUTDOWN_GRACE is read after the drain begins — long after
// the scrub.
func (s *EnvScrubSuite) TestNonSecretConfigurationSurvives() {
	s.T().Setenv("DATABASE_URL", "postgres://tenant:hunter2@10.0.0.5:5432/tenant_x")
	s.T().Setenv("SHUTDOWN_GRACE", "9m")
	s.T().Setenv("DATA_DIR", "/data")
	s.T().Setenv("PORT", "8080")

	scrubProcessSecrets()

	s.Equal("9m", os.Getenv("SHUTDOWN_GRACE"))
	s.Equal("/data", os.Getenv("DATA_DIR"))
	s.Equal("8080", os.Getenv("PORT"))
	s.Empty(os.Getenv("DATABASE_URL"))
}
