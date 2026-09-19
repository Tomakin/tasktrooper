package runtime

import (
	httpadapter "github.com/makifbaysal/tasktrooper/server/internal/adapter/http"
	"github.com/makifbaysal/tasktrooper/server/internal/application/branchflow"
)

// branchFlowHandlerControl keeps a nil service a nil interface, so the handler
// mounts no branch-flow routes instead of routes that dereference nil.
func branchFlowHandlerControl(svc *branchflow.Service) httpadapter.BranchFlowControl {
	if svc == nil {
		return nil
	}
	return svc
}
