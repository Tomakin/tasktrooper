// Package branchflow runs a repository's two-stage delivery.
//
// A review that passes does not hand the task to QA directly: the move out of
// code_review is held, this package merges the branch into the integration
// branch (development) with a merge commit, watches the deploy that push
// starts — it never triggers one — and moves the card to ready_for_qa once
// that deploy is green. QA, and then a human in human_uat, take it to done;
// there the board's own gated merge lands the task's pull request on the
// release branch, and the deploy that push starts is watched to released.
//
// Nothing here is an agent: every step is deterministic and re-derivable from
// GitHub, so the sweeper can be restarted at any point and pick up where the
// last pass stopped.
package branchflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
	"github.com/makifbaysal/tasktrooper/server/internal/port"
)

const (
	DefaultSweepInterval = 30 * time.Second
	// noRunGrace is how long after the merge a push with no workflow run is
	// still "pending" rather than "this branch has no deploy". GitHub queues a
	// push run within seconds; minutes of nothing means nothing is coming.
	noRunGrace   = 10 * time.Minute
	sweepTimeout = 2 * time.Minute
)

type TaskReader interface {
	Get(ctx context.Context, repositoryID, taskID uuid.UUID) (domain.BoardTask, error)
	ListByRepository(ctx context.Context, repositoryID uuid.UUID) ([]domain.BoardTask, error)
}

// Board is the move and comment half of the repository service. Moves go
// through it, not the store, so every gate a human move passes applies here.
type Board interface {
	UpdateTask(ctx context.Context, repositoryID, taskID uuid.UUID, req domain.UpdateBoardTaskRequest) (domain.BoardTask, error)
	AddComment(ctx context.Context, repositoryID, taskID uuid.UUID, req domain.CreateTaskCommentRequest) (domain.TaskComment, error)
}

// ReleaseMerger is the board's gated merge into the default branch
// (board.TaskPRService).
type ReleaseMerger interface {
	MergeTaskPullRequest(ctx context.Context, repositoryID, taskID uuid.UUID) (domain.TaskPRMergeResult, error)
}

type PullRequestReader interface {
	GetPullRequest(ctx context.Context, token, owner, repo string, number int) (port.PullRequest, error)
}

type Deps struct {
	Flows       port.BranchFlowStore
	Tasks       TaskReader
	Board       Board
	Merger      ReleaseMerger
	PRs         PullRequestReader
	GitHub      port.IntegrationGitHub
	Tokens      func(ctx context.Context) (string, error)
	Coordinates func(ctx context.Context, repositoryID uuid.UUID) (owner, repo string, err error)
	// DefaultBranch reports a repository's GitHub default branch, used as the
	// release branch when none is configured.
	DefaultBranch func(ctx context.Context, repositoryID uuid.UUID) string
}

type Service struct {
	flows         port.BranchFlowStore
	tasks         TaskReader
	board         Board
	merger        ReleaseMerger
	prs           PullRequestReader
	gh            port.IntegrationGitHub
	tokens        func(ctx context.Context) (string, error)
	coords        func(ctx context.Context, repositoryID uuid.UUID) (string, string, error)
	defaultBranch func(ctx context.Context, repositoryID uuid.UUID) string
	now           func() time.Time

	// mu keeps a sweep and a release from acting on the same task at once.
	mu sync.Mutex
}

func New(deps Deps) *Service {
	return &Service{
		flows:         deps.Flows,
		tasks:         deps.Tasks,
		board:         deps.Board,
		merger:        deps.Merger,
		prs:           deps.PRs,
		gh:            deps.GitHub,
		tokens:        deps.Tokens,
		coords:        deps.Coordinates,
		defaultBranch: deps.DefaultBranch,
		now:           time.Now,
	}
}

func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Enabled reports whether the repository runs the two-stage flow. The board
// asks it to decide where a passing QA round sends the task and which column
// counts as UAT; any error reads as "no", which is the ordinary flow.
func (s *Service) Enabled(ctx context.Context, repositoryID uuid.UUID) bool {
	if s == nil || s.flows == nil {
		return false
	}
	_, err := s.flows.GetFlow(ctx, repositoryID)
	return err == nil
}

func (s *Service) Flow(ctx context.Context, repositoryID uuid.UUID) (domain.BranchFlow, error) {
	return s.flows.GetFlow(ctx, repositoryID)
}

// A conservative subset of git check-ref-format: enough to refuse what would
// be a typo or an injection, without rejecting a real branch name.
var branchNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// SetFlow turns the flow on with the given integration branch, or off when the
// branch is empty.
func (s *Service) SetFlow(ctx context.Context, repositoryID uuid.UUID, integrationBranch, releaseBranch string) (domain.BranchFlow, error) {
	branch := strings.TrimSpace(integrationBranch)
	if branch == "" {
		return domain.BranchFlow{}, s.flows.DeleteFlow(ctx, repositoryID)
	}
	release := strings.TrimSpace(releaseBranch)
	for _, b := range []string{branch, release} {
		if b == "" {
			continue
		}
		if len(b) > 200 || !branchNamePattern.MatchString(b) || strings.Contains(b, "..") ||
			strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".lock") {
			return domain.BranchFlow{}, fmt.Errorf("%q is not a valid branch name", b)
		}
	}
	if release != "" && release == branch {
		return domain.BranchFlow{}, fmt.Errorf("the integration branch and the release branch cannot both be %q", branch)
	}
	return s.flows.SetFlow(ctx, domain.BranchFlow{
		RepositoryID: repositoryID, IntegrationBranch: branch, ReleaseBranch: release,
	})
}

// IsHeld reports whether this task's next move belongs to the flow.
func (s *Service) IsHeld(ctx context.Context, taskID uuid.UUID) bool {
	if s == nil || s.flows == nil {
		return false
	}
	rec, err := s.flows.GetTaskIntegration(ctx, taskID)
	return err == nil && rec.PromoteTo != ""
}

func (s *Service) TaskIntegration(ctx context.Context, repositoryID, taskID uuid.UUID) (domain.TaskIntegration, error) {
	rec, err := s.flows.GetTaskIntegration(ctx, taskID)
	if err != nil {
		return domain.TaskIntegration{}, err
	}
	if rec.RepositoryID != repositoryID {
		return domain.TaskIntegration{}, domain.ErrTaskIntegrationNotFound
	}
	return rec, nil
}

// Start runs Sweep every interval until ctx ends.
func (s *Service) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepCtx, cancel := context.WithTimeout(ctx, sweepTimeout)
				if err := s.Sweep(sweepCtx); err != nil {
					log.Warn().Err(err).Msg("branch flow sweep failed")
				}
				cancel()
			}
		}
	}()
}

// Sweep moves every task the flow is responsible for one step further: the
// ones held after code review, and the ones in done waiting to be released.
func (s *Service) Sweep(ctx context.Context) error {
	flows, err := s.flows.ListFlows(ctx)
	if err != nil {
		return err
	}
	if len(flows) == 0 {
		return nil
	}
	token := s.token(ctx)
	for _, flow := range flows {
		tasks, err := s.tasks.ListByRepository(ctx, flow.RepositoryID)
		if err != nil {
			log.Warn().Err(err).Str("repository_id", flow.RepositoryID.String()).Msg("branch flow: listing tasks failed")
			continue
		}
		for _, task := range tasks {
			s.mu.Lock()
			switch task.Column {
			case domain.TaskColumnCodeReview:
				s.advanceHeld(ctx, flow, task, token)
			case domain.TaskColumnDone:
				s.advanceRelease(ctx, flow, task, token)
			}
			s.mu.Unlock()
		}
	}
	return nil
}

// HoldReviewPromotion holds a passing review where it is: the card leaves
// code_review only once its change is on the integration branch and that
// deploy is green, which is what "ready for QA" means on a repository with a
// test environment. The system's own move (Sweep) is never held.
func (s *Service) HoldReviewPromotion(ctx context.Context, task domain.BoardTask, from, to domain.TaskColumn, actor domain.TaskActor) bool {
	if s == nil || s.flows == nil || actor == domain.TaskActorSystem {
		return false
	}
	if from != domain.TaskColumnCodeReview || to != domain.TaskColumnReadyForQA {
		return false
	}
	flow, err := s.flows.GetFlow(ctx, task.RepositoryID)
	if err != nil {
		return false
	}
	prev, err := s.flows.GetTaskIntegration(ctx, task.ID)
	if err != nil && !errors.Is(err, domain.ErrTaskIntegrationNotFound) {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: reading task integration failed")
		return false
	}
	rec := prev
	rec.TaskID = task.ID
	rec.RepositoryID = task.RepositoryID
	rec.Branch = flow.IntegrationBranch
	rec.PromoteTo = to
	if _, err := s.flows.SaveTaskIntegration(ctx, rec); err != nil {
		// Holding a card the sweeper will never pick up would strand it, so a
		// bookkeeping failure lets the ordinary move through instead.
		log.Error().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: recording the held review failed; letting the move through")
		return false
	}
	log.Info().Str("task_id", task.ID.String()).Str("branch", flow.IntegrationBranch).
		Msg("review passed; holding the task until its change is on the integration branch")
	return true
}

// advanceHeld carries a task held after code review to ready_for_qa.
func (s *Service) advanceHeld(ctx context.Context, flow domain.BranchFlow, task domain.BoardTask, token string) {
	prev, err := s.flows.GetTaskIntegration(ctx, task.ID)
	if errors.Is(err, domain.ErrTaskIntegrationNotFound) || (err == nil && prev.PromoteTo == "") {
		return
	}
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: reading task integration failed")
		return
	}
	rec := prev
	rec.TaskID = task.ID
	rec.RepositoryID = task.RepositoryID
	rec.Branch = flow.IntegrationBranch

	fail := func(reason domain.IntegrationReason, detail string) {
		rec.Status = domain.IntegrationFailed
		rec.Reason = reason
		rec.Detail = detail
		s.save(ctx, task, prev, rec)
	}

	if token == "" {
		fail(domain.IntegrationReasonNoToken, "GitHub is not connected, so the task cannot be merged into "+flow.IntegrationBranch+".")
		return
	}
	number := task.PRNumber
	if number <= 0 {
		number, _ = domain.ParsePullRequestNumber(task.PRURL)
	}
	if number <= 0 {
		fail(domain.IntegrationReasonNoPullRequest, "The task has no pull request, so there is no branch to merge into "+flow.IntegrationBranch+".")
		return
	}
	owner, repo, err := s.coords(ctx, task.RepositoryID)
	if err != nil {
		fail(domain.IntegrationReasonNoCoordinates, "The repository's GitHub owner/name could not be resolved: "+err.Error())
		return
	}
	taskPR, err := s.prs.GetPullRequest(ctx, token, owner, repo, number)
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: reading the task pull request failed")
		return
	}
	if taskPR.Merged || strings.EqualFold(taskPR.State, "closed") {
		fail(domain.IntegrationReasonPRClosed, fmt.Sprintf("The task's pull request #%d is no longer open.", number))
		return
	}

	if rec.Status == domain.IntegrationMerged && rec.HeadSHA == taskPR.HeadSHA {
		rec = s.watchDeploy(ctx, task, prev, rec, token, owner, repo)
	} else {
		rec = s.mergeIntoIntegration(ctx, flow, task, prev, rec, taskPR, token, owner, repo)
	}
	if rec.IntegrationDeployDone() && rec.PromoteTo != "" {
		s.promote(ctx, task, rec)
	}
}

// promote performs the move the hold deferred, then forgets it.
func (s *Service) promote(ctx context.Context, task domain.BoardTask, rec domain.TaskIntegration) {
	column := rec.PromoteTo
	if _, err := s.board.UpdateTask(ctx, task.RepositoryID, task.ID, domain.UpdateBoardTaskRequest{
		Column:       &column,
		SystemReason: "the change is on " + rec.Branch + " and its deploy finished",
	}); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Str("column", string(column)).
			Msg("branch flow: promoting the held task failed")
		return
	}
	rec.PromoteTo = ""
	if _, err := s.flows.SaveTaskIntegration(ctx, rec); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: clearing the hold failed")
	}
	log.Info().Str("task_id", task.ID.String()).Str("column", string(column)).Msg("branch flow: task promoted")
}

func (s *Service) mergeIntoIntegration(ctx context.Context, flow domain.BranchFlow, task domain.BoardTask,
	prev, rec domain.TaskIntegration, taskPR port.PullRequest, token, owner, repo string) domain.TaskIntegration {
	branch := flow.IntegrationBranch
	head := taskPR.HeadRef

	ipr, found, err := s.gh.FindOpenPullRequest(ctx, token, owner, repo, head, branch)
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: looking up the integration pull request failed")
		return rec
	}
	if !found {
		ipr, err = s.gh.CreatePullRequestInto(ctx, token, owner, repo, head, branch,
			integrationPRTitle(task, taskPR, branch), integrationPRBody(task, taskPR))
		if err != nil {
			rec.Status = domain.IntegrationFailed
			rec.Reason = domain.IntegrationReasonOpenFailed
			rec.Detail = "Opening the pull request into " + branch + " failed: " + err.Error()
			s.save(ctx, task, prev, rec)
			return rec
		}
	} else if fresh, err := s.prs.GetPullRequest(ctx, token, owner, repo, ipr.Number); err == nil {
		// The list endpoint carries no mergeable_state; only a single read does.
		ipr = fresh
	}
	rec.PRNumber = ipr.Number
	rec.PRURL = ipr.HTMLURL
	rec.HeadSHA = ipr.HeadSHA
	rec.MergeSHA = ""
	rec.MergedAt = nil
	rec.DeployStatus = ""
	rec.DeployURL = ""

	switch state := strings.ToLower(strings.TrimSpace(ipr.MergeableState)); state {
	case "clean", "has_hooks", "unstable":
		title := fmt.Sprintf("Merge %s into %s", taskLabel(task), branch)
		sha, err := s.gh.MergeWithMergeCommit(ctx, token, owner, repo, ipr.Number, ipr.HeadSHA, title)
		if err != nil {
			rec.Status = domain.IntegrationWaiting
			rec.Reason = domain.IntegrationReasonMergeRefused
			rec.Detail = err.Error()
			s.save(ctx, task, prev, rec)
			return rec
		}
		now := s.now()
		rec.Status = domain.IntegrationMerged
		rec.MergeSHA = sha
		rec.MergedAt = &now
		rec.DeployStatus = domain.IntegrationDeployPending
		rec.Reason = ""
		rec.Detail = ""
		s.save(ctx, task, prev, rec)
	case "dirty":
		rec.Status = domain.IntegrationConflict
		rec.Reason = domain.IntegrationReasonConflict
		rec.Detail = fmt.Sprintf("The branch conflicts with %s (pull request #%d).", branch, ipr.Number)
		s.save(ctx, task, prev, rec)
		s.sendBack(ctx, task, branch, ipr)
	case "blocked", "behind":
		rec.Status = domain.IntegrationWaiting
		rec.Reason = domain.IntegrationReasonChecksPending
		rec.Detail = fmt.Sprintf("GitHub reports the pull request into %s as `%s`: a required check or review is still pending, or %s requires an up-to-date branch.", branch, state, branch)
		s.save(ctx, task, prev, rec)
	default:
		rec.Status = domain.IntegrationWaiting
		rec.Reason = domain.IntegrationReasonComputing
		rec.Detail = "GitHub is still computing whether the pull request into " + branch + " can be merged."
		s.save(ctx, task, prev, rec)
	}
	return rec
}

// sendBack returns a task whose branch conflicts with the integration branch
// to its developer. The note says what NOT to do as much as what to do: merging
// the integration branch into the task branch would carry every other
// unapproved change on it into the default branch with this task.
func (s *Service) sendBack(ctx context.Context, task domain.BoardTask, branch string, ipr port.PullRequest) {
	note := fmt.Sprintf("Merging this task into `%s` failed: the branch conflicts with it (%s). "+
		"Do not merge `%s` into the task branch — that would carry other, unapproved work to the default branch with this task. "+
		"Rework the change so it applies cleanly, or have a human resolve the conflict on `%s`, then send the task through review again.",
		branch, ipr.HTMLURL, branch, branch)
	if _, err := s.board.AddComment(ctx, task.RepositoryID, task.ID, domain.CreateTaskCommentRequest{Content: note, AuthorType: "system"}); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: conflict comment failed")
	}
	column := domain.TaskColumnNeedRevision
	if _, err := s.board.UpdateTask(ctx, task.RepositoryID, task.ID, domain.UpdateBoardTaskRequest{Column: &column}); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: moving the conflicting task to need_revision failed")
	}
}

func (s *Service) watchDeploy(ctx context.Context, task domain.BoardTask, prev, rec domain.TaskIntegration, token, owner, repo string) domain.TaskIntegration {
	if rec.DeployStatus == domain.IntegrationDeploySuccess || rec.DeployStatus == domain.IntegrationDeployFailure ||
		rec.DeployStatus == domain.IntegrationDeployNone {
		return rec
	}
	runs, err := s.gh.ListPushRuns(ctx, token, owner, repo, rec.Branch, rec.MergeSHA)
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: listing push runs failed")
		return rec
	}
	rec.DeployStatus, rec.DeployURL = deployVerdict(runs)
	if rec.DeployStatus == "" {
		rec.DeployStatus = domain.IntegrationDeployPending
		if rec.MergedAt != nil && s.now().Sub(*rec.MergedAt) >= noRunGrace {
			rec.DeployStatus = domain.IntegrationDeployNone
		}
	}
	s.save(ctx, task, prev, rec)
	return rec
}

// deployVerdict folds the push's runs into one answer: any run still going is
// pending, any failed run is a failure, otherwise success. No runs is "" —
// the caller decides between "not yet" and "never".
func deployVerdict(runs []port.ActionsRun) (domain.IntegrationDeployStatus, string) {
	if len(runs) == 0 {
		return "", ""
	}
	url := runs[0].HTMLURL
	failed := ""
	for _, r := range runs {
		if !strings.EqualFold(r.Status, "completed") {
			return domain.IntegrationDeployPending, r.HTMLURL
		}
		switch strings.ToLower(r.Conclusion) {
		case "success", "skipped", "neutral":
		default:
			if failed == "" {
				failed = r.HTMLURL
			}
		}
	}
	if failed != "" {
		return domain.IntegrationDeployFailure, failed
	}
	return domain.IntegrationDeploySuccess, url
}

// save persists the record and, when what a human would care about changed,
// writes one comment on the card saying so.
func (s *Service) save(ctx context.Context, task domain.BoardTask, prev, rec domain.TaskIntegration) {
	if _, err := s.flows.SaveTaskIntegration(ctx, rec); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: saving task integration failed")
		return
	}
	note := transitionNote(prev, rec)
	if note == "" || s.board == nil {
		return
	}
	if _, err := s.board.AddComment(ctx, task.RepositoryID, task.ID, domain.CreateTaskCommentRequest{Content: note, AuthorType: "system"}); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: status comment failed")
	}
}

func transitionNote(prev, rec domain.TaskIntegration) string {
	sameHead := prev.HeadSHA == rec.HeadSHA
	if sameHead && prev.Status == rec.Status && prev.DeployStatus == rec.DeployStatus {
		return ""
	}
	switch rec.Status {
	case domain.IntegrationMerged:
		if !sameHead || prev.Status != domain.IntegrationMerged {
			return fmt.Sprintf("Merged into `%s` as %s (%s). Watching the deploy that push started.",
				rec.Branch, domain.ShortSHA(rec.MergeSHA), rec.PRURL)
		}
		switch rec.DeployStatus {
		case domain.IntegrationDeploySuccess:
			return fmt.Sprintf("The `%s` deploy for %s succeeded (%s). Test it there, then approve or request a revision.",
				rec.Branch, domain.ShortSHA(rec.MergeSHA), rec.DeployURL)
		case domain.IntegrationDeployFailure:
			return fmt.Sprintf("The `%s` deploy for %s FAILED (%s). The task cannot be released until it is fixed.",
				rec.Branch, domain.ShortSHA(rec.MergeSHA), rec.DeployURL)
		case domain.IntegrationDeployNone:
			return fmt.Sprintf("No workflow ran for the push to `%s`, so there is no deploy to wait for. Test the change there, then approve or request a revision.",
				rec.Branch)
		}
	case domain.IntegrationFailed:
		return "Merging into `" + rec.Branch + "` is stuck: " + rec.Detail
	}
	return ""
}

// advanceRelease lands a signed-off task on the release branch and follows it
// to production. Two steps, both re-derivable from GitHub, so a restart picks
// up wherever the last pass stopped: merge the task's own pull request through
// the board's gates, then watch the workflow runs that merge push started.
func (s *Service) advanceRelease(ctx context.Context, flow domain.BranchFlow, task domain.BoardTask, token string) {
	prev, err := s.flows.GetTaskIntegration(ctx, task.ID)
	if err != nil && !errors.Is(err, domain.ErrTaskIntegrationNotFound) {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: reading task integration failed")
		return
	}
	rec := prev
	rec.TaskID = task.ID
	rec.RepositoryID = task.RepositoryID
	if rec.Branch == "" {
		rec.Branch = flow.IntegrationBranch
	}

	mergeSHA := strings.TrimSpace(task.MergeCommitSHA)
	if mergeSHA == "" {
		if s.merger == nil {
			return
		}
		merge, err := s.merger.MergeTaskPullRequest(ctx, task.RepositoryID, task.ID)
		if err != nil {
			// Every refusal names its own rule (an incomplete review chain, a
			// red pipeline, a head that is not the verified one). It is the
			// human's to resolve, so it is said once and not retried into.
			if prev.Reason != domain.IntegrationReasonReleaseRefused || prev.Detail != err.Error() {
				rec.Reason = domain.IntegrationReasonReleaseRefused
				rec.Detail = err.Error()
				s.save(ctx, task, prev, rec)
				s.comment(ctx, task, "Merging this task into the release branch was refused: "+err.Error())
			}
			return
		}
		mergeSHA = merge.MergeCommitSHA
		rec.Reason = ""
		rec.Detail = ""
		rec.ReleaseDeployStatus = domain.IntegrationDeployPending
		s.save(ctx, task, prev, rec)
		s.comment(ctx, task, fmt.Sprintf("Merged into `%s` as %s. Watching the deploy that push started.",
			s.releaseBranchName(ctx, flow, task), domain.ShortSHA(mergeSHA)))
		prev = rec
	}

	if rec.ReleaseDeployDone() {
		s.release(ctx, task, rec)
		return
	}
	owner, repo, err := s.coords(ctx, task.RepositoryID)
	if err != nil || token == "" {
		return
	}
	branch := s.releaseBranchName(ctx, flow, task)
	runs, err := s.gh.ListPushRuns(ctx, token, owner, repo, branch, mergeSHA)
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: listing release push runs failed")
		return
	}
	status, url := deployVerdict(runs)
	if status == "" {
		status = domain.IntegrationDeployPending
		if rec.MergedAt != nil && s.now().Sub(*rec.MergedAt) >= noRunGrace {
			status = domain.IntegrationDeployNone
		}
	}
	if status == rec.ReleaseDeployStatus {
		return
	}
	rec.ReleaseDeployStatus = status
	rec.ReleaseDeployURL = url
	if _, err := s.flows.SaveTaskIntegration(ctx, rec); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: saving the release deploy status failed")
		return
	}
	switch status {
	case domain.IntegrationDeployFailure:
		s.comment(ctx, task, fmt.Sprintf("The `%s` deploy for %s FAILED (%s). The task stays in done until it is fixed.",
			branch, domain.ShortSHA(mergeSHA), url))
	case domain.IntegrationDeploySuccess, domain.IntegrationDeployNone:
		s.release(ctx, task, rec)
	}
}

// release is the last move: the change is on the release branch and whatever
// deploy that push started has finished.
func (s *Service) release(ctx context.Context, task domain.BoardTask, rec domain.TaskIntegration) {
	column := domain.TaskColumnReleased
	if _, err := s.board.UpdateTask(ctx, task.RepositoryID, task.ID, domain.UpdateBoardTaskRequest{
		Column:       &column,
		SystemReason: "merged into the release branch and deployed",
	}); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: moving the task to released failed")
		return
	}
	log.Info().Str("task_id", task.ID.String()).Msg("branch flow: task released")
}

// releaseBranchName is the configured release branch, or the repository's
// GitHub default branch when none is configured.
func (s *Service) releaseBranchName(ctx context.Context, flow domain.BranchFlow, task domain.BoardTask) string {
	if b := strings.TrimSpace(flow.ReleaseBranch); b != "" {
		return b
	}
	if s.defaultBranch == nil {
		return ""
	}
	return s.defaultBranch(ctx, task.RepositoryID)
}

// ReleaseBranchForWorkspace answers the git client's question — which branch is
// this checkout's base? — from a task workspace path (`task-<uuid>`). Empty
// means "the repository's default branch", which is what every caller did
// before a release branch could be configured.
func (s *Service) ReleaseBranchForWorkspace(ctx context.Context, workspacePath string) string {
	if s == nil || s.flows == nil {
		return ""
	}
	name := filepath.Base(strings.TrimRight(workspacePath, string(filepath.Separator)))
	rest, ok := strings.CutPrefix(name, "task-")
	if !ok {
		return ""
	}
	taskID, err := uuid.Parse(rest)
	if err != nil {
		return ""
	}
	repositoryID, err := s.flows.TaskRepository(ctx, taskID)
	if err != nil {
		return ""
	}
	flow, err := s.flows.GetFlow(ctx, repositoryID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(flow.ReleaseBranch)
}

func (s *Service) comment(ctx context.Context, task domain.BoardTask, body string) {
	if s.board == nil {
		return
	}
	if _, err := s.board.AddComment(ctx, task.RepositoryID, task.ID,
		domain.CreateTaskCommentRequest{Content: body, AuthorType: "system"}); err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: comment failed")
	}
}

func (s *Service) token(ctx context.Context) string {
	if s.tokens == nil {
		return ""
	}
	t, err := s.tokens(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(t)
}

func taskLabel(task domain.BoardTask) string {
	if strings.TrimSpace(task.Key) != "" {
		return task.Key
	}
	return task.ID.String()
}

func integrationPRTitle(task domain.BoardTask, taskPR port.PullRequest, branch string) string {
	title := strings.TrimSpace(taskPR.Title)
	if title == "" {
		title = strings.TrimSpace(task.Title)
	}
	return fmt.Sprintf("[%s] %s", branch, title)
}

func integrationPRBody(task domain.BoardTask, taskPR port.PullRequest) string {
	lines := []string{
		"Opened by TaskTrooper's branch flow to put this task on the integration environment for testing.",
		"It is merged automatically; the change reaches the default branch through its own pull request once a human approves it.",
		"",
	}
	if key := strings.TrimSpace(task.Key); key != "" {
		lines = append(lines, "Task: "+key+" "+strings.TrimSpace(task.Title))
	}
	if taskPR.HTMLURL != "" {
		lines = append(lines, "Pull request: "+taskPR.HTMLURL)
	}
	return strings.Join(lines, "\n")
}
