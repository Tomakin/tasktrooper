package runtime

import (
	"github.com/makifbaysal/tasktrooper/server/internal/adapter/agentcli/claudecode"
	httpadapter "github.com/makifbaysal/tasktrooper/server/internal/adapter/http"
	"github.com/makifbaysal/tasktrooper/server/internal/application/agentconcurrency"
)

// initialSessionLimit maps claude_code.max_concurrent_sessions onto the
// limiter's terms: 0 is the executor's default, negative is unlimited.
func initialSessionLimit(configured int) int {
	switch {
	case configured == 0:
		return claudecode.DefaultMaxConcurrentSessions
	case configured < 0:
		return 0
	default:
		return configured
	}
}

// agentConcurrencyControl keeps a nil service a nil interface, so the handler
// mounts no routes instead of routes that dereference nil.
func agentConcurrencyControl(svc *agentconcurrency.Service) httpadapter.AgentConcurrencyControl {
	if svc == nil {
		return nil
	}
	return svc
}
