// Package branchflow runs a repository's two-stage delivery. A task that
// passes QA lands in human_uat; from there this package merges its branch into
// the integration branch (development) with a merge commit, watches the deploy
// that push starts — it never triggers one — and, once a human approves the
// task there, moves it to done and merges its own pull request into the
// default branch through the board's gated merge.
//
// Nothing here is an agent: every step is deterministic and re-derivable from
// GitHub, so the sweeper can be restarted at any point and pick up where the
// last pass stopped.
package branchflow

import (
	"context"
	"errors"
	"fmt"
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
}

type Service struct {
	flows  port.BranchFlowStore
	tasks  TaskReader
	board  Board
	merger ReleaseMerger
	prs    PullRequestReader
	gh     port.IntegrationGitHub
	tokens func(ctx context.Context) (string, error)
	coords func(ctx context.Context, repositoryID uuid.UUID) (string, string, error)
	now    func() time.Time

	// mu keeps a sweep and a release from acting on the same task at once.
	mu sync.Mutex
}

func New(deps Deps) *Service {
	return &Service{
		flows:  deps.Flows,
		tasks:  deps.Tasks,
		board:  deps.Board,
		merger: deps.Merger,
		prs:    deps.PRs,
		gh:     deps.GitHub,
		tokens: deps.Tokens,
		coords: deps.Coordinates,
		now:    time.Now,
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
func (s *Service) SetFlow(ctx context.Context, repositoryID uuid.UUID, integrationBranch string) (domain.BranchFlow, error) {
	branch := strings.TrimSpace(integrationBranch)
	if branch == "" {
		return domain.BranchFlow{}, s.flows.DeleteFlow(ctx, repositoryID)
	}
	if len(branch) > 200 || !branchNamePattern.MatchString(branch) || strings.Contains(branch, "..") ||
		strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".lock") {
		return domain.BranchFlow{}, fmt.Errorf("%q is not a valid branch name", branch)
	}
	return s.flows.SetFlow(ctx, domain.BranchFlow{RepositoryID: repositoryID, IntegrationBranch: branch})
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

// Sweep advances every human_uat task of every repository with a flow.
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
			if task.Column != domain.TaskColumnHumanUAT {
				continue
			}
			s.mu.Lock()
			s.advance(ctx, flow, task, token)
			s.mu.Unlock()
		}
	}
	return nil
}

func (s *Service) advance(ctx context.Context, flow domain.BranchFlow, task domain.BoardTask, token string) {
	prev, err := s.flows.GetTaskIntegration(ctx, task.ID)
	if err != nil && !errors.Is(err, domain.ErrTaskIntegrationNotFound) {
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
		s.watchDeploy(ctx, task, prev, rec, token, owner, repo)
		return
	}
	s.mergeIntoIntegration(ctx, flow, task, prev, rec, taskPR, token, owner, repo)
}

func (s *Service) mergeIntoIntegration(ctx context.Context, flow domain.BranchFlow, task domain.BoardTask,
	prev, rec domain.TaskIntegration, taskPR port.PullRequest, token, owner, repo string) {
	branch := flow.IntegrationBranch
	head := taskPR.HeadRef

	ipr, found, err := s.gh.FindOpenPullRequest(ctx, token, owner, repo, head, branch)
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: looking up the integration pull request failed")
		return
	}
	if !found {
		ipr, err = s.gh.CreatePullRequestInto(ctx, token, owner, repo, head, branch,
			integrationPRTitle(task, taskPR, branch), integrationPRBody(task, taskPR))
		if err != nil {
			rec.Status = domain.IntegrationFailed
			rec.Reason = domain.IntegrationReasonOpenFailed
			rec.Detail = "Opening the pull request into " + branch + " failed: " + err.Error()
			s.save(ctx, task, prev, rec)
			return
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
			return
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

func (s *Service) watchDeploy(ctx context.Context, task domain.BoardTask, prev, rec domain.TaskIntegration, token, owner, repo string) {
	if rec.DeployStatus == domain.IntegrationDeploySuccess || rec.DeployStatus == domain.IntegrationDeployFailure ||
		rec.DeployStatus == domain.IntegrationDeployNone {
		return
	}
	runs, err := s.gh.ListPushRuns(ctx, token, owner, repo, rec.Branch, rec.MergeSHA)
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("branch flow: listing push runs failed")
		return
	}
	rec.DeployStatus, rec.DeployURL = deployVerdict(runs)
	if rec.DeployStatus == "" {
		rec.DeployStatus = domain.IntegrationDeployPending
		if rec.MergedAt != nil && s.now().Sub(*rec.MergedAt) >= noRunGrace {
			rec.DeployStatus = domain.IntegrationDeployNone
		}
	}
	s.save(ctx, task, prev, rec)
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

type ReleaseResult struct {
	Task       domain.BoardTask          `json:"task"`
	Merge      *domain.TaskPRMergeResult `json:"merge,omitempty"`
	MergeError string                    `json:"merge_error,omitempty"`
}

// Release is the human's "tested on the integration environment, ship it". It
// refuses unless the task's current head is on the integration branch and that
// deploy did not fail, then moves the task to done — through every gate a move
// into done has — and merges its pull request into the default branch.
//
// A refused or failed merge leaves the task in done and says why on the card:
// the approval stands, and the board's own done-column merge (the QA agent)
// is the retry path.
func (s *Service) Release(ctx context.Context, repositoryID, taskID uuid.UUID) (ReleaseResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.flows.GetFlow(ctx, repositoryID); err != nil {
		return ReleaseResult{}, err
	}
	task, err := s.tasks.Get(ctx, repositoryID, taskID)
	if err != nil {
		return ReleaseResult{}, err
	}
	if task.Column != domain.TaskColumnHumanUAT {
		return ReleaseResult{}, fmt.Errorf("%w: it is in `%s`, not human_uat", domain.ErrReleaseNotReady, task.Column)
	}
	rec, err := s.flows.GetTaskIntegration(ctx, taskID)
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("%w: it has not been merged into the integration branch yet", domain.ErrReleaseNotReady)
	}
	head, err := s.currentHead(ctx, task)
	if err != nil {
		return ReleaseResult{}, err
	}
	if !rec.ReadyForRelease(head) {
		return ReleaseResult{}, fmt.Errorf("%w: %s", domain.ErrReleaseNotReady, notReadyReason(rec, head))
	}

	done := domain.TaskColumnDone
	moved, err := s.board.UpdateTask(ctx, repositoryID, taskID, domain.UpdateBoardTaskRequest{Column: &done})
	if err != nil {
		return ReleaseResult{}, err
	}
	out := ReleaseResult{Task: moved}
	merge, err := s.merger.MergeTaskPullRequest(ctx, repositoryID, taskID)
	if err != nil {
		out.MergeError = err.Error()
		note := "Approved after testing on `" + rec.Branch + "`, but merging into the default branch was refused: " + err.Error()
		if _, cerr := s.board.AddComment(ctx, repositoryID, taskID, domain.CreateTaskCommentRequest{Content: note, AuthorType: "system"}); cerr != nil {
			log.Warn().Err(cerr).Str("task_id", taskID.String()).Msg("branch flow: release refusal comment failed")
		}
		return out, nil
	}
	out.Merge = &merge
	if fresh, err := s.tasks.Get(ctx, repositoryID, taskID); err == nil {
		out.Task = fresh
	}
	return out, nil
}

func (s *Service) currentHead(ctx context.Context, task domain.BoardTask) (string, error) {
	token := s.token(ctx)
	if token == "" {
		return "", fmt.Errorf("%w: GitHub is not connected", domain.ErrReleaseNotReady)
	}
	number := task.PRNumber
	if number <= 0 {
		number, _ = domain.ParsePullRequestNumber(task.PRURL)
	}
	if number <= 0 {
		return "", fmt.Errorf("%w: the task has no pull request", domain.ErrReleaseNotReady)
	}
	owner, repo, err := s.coords(ctx, task.RepositoryID)
	if err != nil {
		return "", err
	}
	pr, err := s.prs.GetPullRequest(ctx, token, owner, repo, number)
	if err != nil {
		return "", err
	}
	return pr.HeadSHA, nil
}

func notReadyReason(rec domain.TaskIntegration, head string) string {
	switch {
	case rec.Status != domain.IntegrationMerged:
		return "it is not merged into `" + rec.Branch + "` (" + string(rec.Status) + ")"
	case rec.HeadSHA != head:
		return "the branch has moved since it was merged into `" + rec.Branch + "`; the new commits have to be merged and tested there first"
	case rec.DeployStatus == domain.IntegrationDeployFailure:
		return "the `" + rec.Branch + "` deploy failed"
	default:
		return "the `" + rec.Branch + "` deploy has not finished"
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
