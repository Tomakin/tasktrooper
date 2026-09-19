package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// BranchFlow is a repository's two-stage delivery: a task that passes QA is
// merged into IntegrationBranch (whose own push deploys a test environment),
// a human tests it there, and only then does its pull request land on the
// default branch. No row means the ordinary one-stage flow.
type BranchFlow struct {
	RepositoryID      uuid.UUID `json:"repository_id"`
	IntegrationBranch string    `json:"integration_branch"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type IntegrationStatus string

const (
	// IntegrationWaiting: the integration PR exists but GitHub will not merge it
	// yet (checks running, branch protection, mergeability not computed).
	IntegrationWaiting IntegrationStatus = "waiting"
	IntegrationMerged  IntegrationStatus = "merged"
	// IntegrationConflict sends the task back to need_revision.
	IntegrationConflict IntegrationStatus = "conflict"
	IntegrationFailed   IntegrationStatus = "failed"
)

type IntegrationReason string

const (
	IntegrationReasonNoToken       IntegrationReason = "no_token"
	IntegrationReasonNoPullRequest IntegrationReason = "no_pull_request"
	IntegrationReasonPRClosed      IntegrationReason = "pull_request_closed"
	IntegrationReasonNoCoordinates IntegrationReason = "no_coordinates"
	IntegrationReasonOpenFailed    IntegrationReason = "open_failed"
	IntegrationReasonMergeRefused  IntegrationReason = "merge_refused"
	IntegrationReasonChecksPending IntegrationReason = "checks_pending"
	IntegrationReasonComputing     IntegrationReason = "computing"
	IntegrationReasonConflict      IntegrationReason = "conflict"
)

type IntegrationDeployStatus string

const (
	IntegrationDeployPending IntegrationDeployStatus = "pending"
	IntegrationDeploySuccess IntegrationDeployStatus = "success"
	IntegrationDeployFailure IntegrationDeployStatus = "failure"
	// IntegrationDeployNone: no workflow ran for the merge push. The branch has
	// no deploy, which is an answer, not an error.
	IntegrationDeployNone IntegrationDeployStatus = "none"
)

// TaskIntegration is where one task stands on its repository's integration
// branch. HeadSHA is the branch head that was (or is being) merged; a task
// whose branch has moved past it has to be merged again.
type TaskIntegration struct {
	TaskID       uuid.UUID         `json:"task_id"`
	RepositoryID uuid.UUID         `json:"repository_id"`
	Branch       string            `json:"branch"`
	PRNumber     int               `json:"pr_number,omitempty"`
	PRURL        string            `json:"pr_url,omitempty"`
	HeadSHA      string            `json:"head_sha,omitempty"`
	MergeSHA     string            `json:"merge_sha,omitempty"`
	Status       IntegrationStatus `json:"status"`
	// Reason is Detail as a code the UI can translate.
	Reason       IntegrationReason       `json:"reason,omitempty"`
	Detail       string                  `json:"detail,omitempty"`
	DeployStatus IntegrationDeployStatus `json:"deploy_status,omitempty"`
	DeployURL    string                  `json:"deploy_url,omitempty"`
	MergedAt     *time.Time              `json:"merged_at,omitempty"`
	UpdatedAt    time.Time               `json:"updated_at"`
}

// ReadyForRelease reports whether a human may approve the task for the default
// branch: its current head is on the integration branch and the deploy that
// push started did not fail.
func (t TaskIntegration) ReadyForRelease(currentHead string) bool {
	if t.Status != IntegrationMerged || t.HeadSHA == "" || t.HeadSHA != currentHead {
		return false
	}
	return t.DeployStatus == IntegrationDeploySuccess || t.DeployStatus == IntegrationDeployNone
}

var (
	ErrBranchFlowNotFound      = errors.New("branch flow not configured for this repository")
	ErrTaskIntegrationNotFound = errors.New("task has no integration record")
	// ErrReleaseNotReady refuses "passed, ship it" for a task whose current
	// change is not verifiably on the integration branch and deployed there.
	ErrReleaseNotReady = errors.New("the task is not ready to be released")
)
