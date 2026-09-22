package port

import (
	"context"

	"github.com/google/uuid"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

type BranchFlowStore interface {
	// GetFlow returns domain.ErrBranchFlowNotFound when the repository has none.
	GetFlow(ctx context.Context, repositoryID uuid.UUID) (domain.BranchFlow, error)
	ListFlows(ctx context.Context) ([]domain.BranchFlow, error)
	SetFlow(ctx context.Context, flow domain.BranchFlow) (domain.BranchFlow, error)
	DeleteFlow(ctx context.Context, repositoryID uuid.UUID) error
	// TaskRepository resolves a task's repository; used where only a task id is
	// known (the git client's base-branch lookup).
	TaskRepository(ctx context.Context, taskID uuid.UUID) (uuid.UUID, error)
	// GetTaskIntegration returns domain.ErrTaskIntegrationNotFound when none.
	GetTaskIntegration(ctx context.Context, taskID uuid.UUID) (domain.TaskIntegration, error)
	SaveTaskIntegration(ctx context.Context, in domain.TaskIntegration) (domain.TaskIntegration, error)
}

// IntegrationGitHub is what the branch flow needs from GitHub beyond the task
// PR client: a second pull request for the same branch into the integration
// branch, merged with a merge commit so the branch can still go to the default
// branch afterwards, and the push runs that merge started.
type IntegrationGitHub interface {
	// FindOpenPullRequest returns the open PR from head into base; ok=false
	// when there is none.
	FindOpenPullRequest(ctx context.Context, token, owner, repo, head, base string) (pr PullRequest, ok bool, err error)
	CreatePullRequestInto(ctx context.Context, token, owner, repo, head, base, title, body string) (PullRequest, error)
	// MergeWithMergeCommit merges exactly expectedHeadSHA or nothing, and keeps
	// the head branch.
	MergeWithMergeCommit(ctx context.Context, token, owner, repo string, number int, expectedHeadSHA, title string) (mergeSHA string, err error)
	// ListPushRuns returns the workflow runs a push of sha to branch started.
	ListPushRuns(ctx context.Context, token, owner, repo, branch, sha string) ([]ActionsRun, error)
}
