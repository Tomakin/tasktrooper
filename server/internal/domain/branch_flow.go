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
	// ReleaseBranch is where a task's own pull request is opened and, at the
	// end, merged. Empty means the repository's GitHub default branch.
	ReleaseBranch string    `json:"release_branch"`
	UpdatedAt     time.Time `json:"updated_at"`
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
	IntegrationReasonNoToken        IntegrationReason = "no_token"
	IntegrationReasonNoPullRequest  IntegrationReason = "no_pull_request"
	IntegrationReasonPRClosed       IntegrationReason = "pull_request_closed"
	IntegrationReasonNoCoordinates  IntegrationReason = "no_coordinates"
	IntegrationReasonOpenFailed     IntegrationReason = "open_failed"
	IntegrationReasonMergeRefused   IntegrationReason = "merge_refused"
	IntegrationReasonChecksPending  IntegrationReason = "checks_pending"
	IntegrationReasonComputing      IntegrationReason = "computing"
	IntegrationReasonConflict       IntegrationReason = "conflict"
	IntegrationReasonReleaseRefused IntegrationReason = "release_refused"
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
	// PromoteTo is the column the card moves to once the change is on the
	// integration branch and that deploy is green. Empty means nothing is held.
	PromoteTo TaskColumn `json:"promote_to,omitempty"`
	// ReleaseDeployStatus follows the deploy the release-branch merge started.
	ReleaseDeployStatus IntegrationDeployStatus `json:"release_deploy_status,omitempty"`
	ReleaseDeployURL    string                  `json:"release_deploy_url,omitempty"`
	UpdatedAt           time.Time               `json:"updated_at"`
}

// IntegrationDeployDone reports whether the integration deploy has reached a
// verdict the flow can act on: green, or no workflow at all.
func (t TaskIntegration) IntegrationDeployDone() bool {
	return t.Status == IntegrationMerged &&
		(t.DeployStatus == IntegrationDeploySuccess || t.DeployStatus == IntegrationDeployNone)
}

// ReleaseDeployDone is IntegrationDeployDone for the release branch.
func (t TaskIntegration) ReleaseDeployDone() bool {
	return t.ReleaseDeployStatus == IntegrationDeploySuccess || t.ReleaseDeployStatus == IntegrationDeployNone
}

var (
	ErrBranchFlowNotFound      = errors.New("branch flow not configured for this repository")
	ErrTaskIntegrationNotFound = errors.New("task has no integration record")
	// ErrReleaseNotReady refuses "passed, ship it" for a task whose current
	// change is not verifiably on the integration branch and deployed there.
	ErrReleaseNotReady = errors.New("the task is not ready to be released")
)
