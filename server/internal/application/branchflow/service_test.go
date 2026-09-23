package branchflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
	"github.com/makifbaysal/tasktrooper/server/internal/port"
)

type memFlows struct {
	flows     map[uuid.UUID]domain.BranchFlow
	recs      map[uuid.UUID]domain.TaskIntegration
	taskRepos map[uuid.UUID]uuid.UUID
}

func (m *memFlows) GetFlow(_ context.Context, id uuid.UUID) (domain.BranchFlow, error) {
	f, ok := m.flows[id]
	if !ok {
		return domain.BranchFlow{}, domain.ErrBranchFlowNotFound
	}
	return f, nil
}
func (m *memFlows) ListFlows(context.Context) ([]domain.BranchFlow, error) {
	var out []domain.BranchFlow
	for _, f := range m.flows {
		out = append(out, f)
	}
	return out, nil
}
func (m *memFlows) SetFlow(_ context.Context, f domain.BranchFlow) (domain.BranchFlow, error) {
	m.flows[f.RepositoryID] = f
	return f, nil
}
func (m *memFlows) DeleteFlow(_ context.Context, id uuid.UUID) error { delete(m.flows, id); return nil }
func (m *memFlows) TaskRepository(_ context.Context, taskID uuid.UUID) (uuid.UUID, error) {
	if repo, ok := m.taskRepos[taskID]; ok {
		return repo, nil
	}
	return uuid.Nil, domain.ErrTaskIntegrationNotFound
}
func (m *memFlows) GetTaskIntegration(_ context.Context, id uuid.UUID) (domain.TaskIntegration, error) {
	r, ok := m.recs[id]
	if !ok {
		return domain.TaskIntegration{}, domain.ErrTaskIntegrationNotFound
	}
	return r, nil
}
func (m *memFlows) SaveTaskIntegration(_ context.Context, r domain.TaskIntegration) (domain.TaskIntegration, error) {
	m.recs[r.TaskID] = r
	return r, nil
}

type fakeBoard struct {
	tasks    map[uuid.UUID]domain.BoardTask
	comments []string
	moveErr  error
}

func (b *fakeBoard) Get(_ context.Context, _, id uuid.UUID) (domain.BoardTask, error) {
	t, ok := b.tasks[id]
	if !ok {
		return domain.BoardTask{}, errors.New("no task")
	}
	return t, nil
}
func (b *fakeBoard) ListByRepository(_ context.Context, repo uuid.UUID) ([]domain.BoardTask, error) {
	var out []domain.BoardTask
	for _, t := range b.tasks {
		if t.RepositoryID == repo {
			out = append(out, t)
		}
	}
	return out, nil
}
func (b *fakeBoard) UpdateTask(_ context.Context, _, id uuid.UUID, req domain.UpdateBoardTaskRequest) (domain.BoardTask, error) {
	if b.moveErr != nil {
		return domain.BoardTask{}, b.moveErr
	}
	t := b.tasks[id]
	if req.Column != nil {
		t.Column = *req.Column
	}
	b.tasks[id] = t
	return t, nil
}
func (b *fakeBoard) AddComment(_ context.Context, _, _ uuid.UUID, req domain.CreateTaskCommentRequest) (domain.TaskComment, error) {
	b.comments = append(b.comments, req.Content)
	return domain.TaskComment{Content: req.Content}, nil
}

type fakeMerger struct {
	calls int
	err   error
}

func (m *fakeMerger) MergeTaskPullRequest(context.Context, uuid.UUID, uuid.UUID) (domain.TaskPRMergeResult, error) {
	m.calls++
	if m.err != nil {
		return domain.TaskPRMergeResult{}, m.err
	}
	return domain.TaskPRMergeResult{Merged: true, MergeCommitSHA: "mainsha"}, nil
}

// fakeGitHub keeps the task PR (#1, into main) and the integration PRs.
type fakeGitHub struct {
	head          string
	nextState     string
	intPRs        map[int]port.PullRequest
	merged        map[int]string
	runs          []port.ActionsRun
	created       int
	mergeCalls    int
	lastRunBranch string
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{head: "h1", nextState: "clean", intPRs: map[int]port.PullRequest{}, merged: map[int]string{}}
}

func (g *fakeGitHub) GetPullRequest(_ context.Context, _, _, _ string, n int) (port.PullRequest, error) {
	if n == 1 {
		return port.PullRequest{Number: 1, State: "open", HeadRef: "feature/a-1", BaseRef: "main", HeadSHA: g.head,
			Title: "feat: thing", HTMLURL: "https://github.com/acme/app/pull/1"}, nil
	}
	pr, ok := g.intPRs[n]
	if !ok {
		return port.PullRequest{}, errors.New("no pr")
	}
	pr.HeadSHA = g.head
	pr.MergeableState = g.nextState
	return pr, nil
}
func (g *fakeGitHub) FindOpenPullRequest(_ context.Context, _, _, _, head, base string) (port.PullRequest, bool, error) {
	for n, pr := range g.intPRs {
		if _, done := g.merged[n]; !done && pr.HeadRef == head && pr.BaseRef == base {
			return pr, true, nil
		}
	}
	return port.PullRequest{}, false, nil
}
func (g *fakeGitHub) CreatePullRequestInto(_ context.Context, _, _, _, head, base, title, _ string) (port.PullRequest, error) {
	g.created++
	n := 100 + g.created
	pr := port.PullRequest{Number: n, State: "open", HeadRef: head, BaseRef: base, HeadSHA: g.head, Title: title,
		MergeableState: "unknown", HTMLURL: "https://github.com/acme/app/pull/" + string(rune('0'+g.created))}
	g.intPRs[n] = pr
	return pr, nil
}
func (g *fakeGitHub) MergeWithMergeCommit(_ context.Context, _, _, _ string, n int, sha, _ string) (string, error) {
	g.mergeCalls++
	if sha != g.head {
		return "", errors.New("head moved")
	}
	g.merged[n] = "m-" + sha
	return "m-" + sha, nil
}
func (g *fakeGitHub) ListPushRuns(_ context.Context, _, _, _, branch, sha string) ([]port.ActionsRun, error) {
	g.lastRunBranch = branch
	return g.runs, nil
}

type fixture struct {
	svc    *Service
	flows  *memFlows
	board  *fakeBoard
	gh     *fakeGitHub
	merger *fakeMerger
	repoID uuid.UUID
	taskID uuid.UUID
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		flows: &memFlows{
			flows:     map[uuid.UUID]domain.BranchFlow{},
			recs:      map[uuid.UUID]domain.TaskIntegration{},
			taskRepos: map[uuid.UUID]uuid.UUID{},
		},
		board:  &fakeBoard{tasks: map[uuid.UUID]domain.BoardTask{}},
		gh:     newFakeGitHub(),
		merger: &fakeMerger{},
		repoID: uuid.New(),
		taskID: uuid.New(),
		now:    time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC),
	}
	f.flows.flows[f.repoID] = domain.BranchFlow{
		RepositoryID: f.repoID, IntegrationBranch: "development", ReleaseBranch: "master",
	}
	f.flows.taskRepos[f.taskID] = f.repoID
	f.board.tasks[f.taskID] = domain.BoardTask{ID: f.taskID, RepositoryID: f.repoID, Key: "A-1", Title: "Thing",
		Column: domain.TaskColumnCodeReview, PRNumber: 1, PRURL: "https://github.com/acme/app/pull/1"}
	f.svc = New(Deps{
		Flows: f.flows, Tasks: f.board, Board: f.board, Merger: f.merger, PRs: f.gh, GitHub: f.gh,
		Tokens:        func(context.Context) (string, error) { return "tok", nil },
		Coordinates:   func(context.Context, uuid.UUID) (string, string, error) { return "acme", "app", nil },
		DefaultBranch: func(context.Context, uuid.UUID) string { return "main" },
	})
	f.svc.SetClock(func() time.Time { return f.now })
	return f
}

func (f *fixture) sweep(t *testing.T) domain.TaskIntegration {
	t.Helper()
	if err := f.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f.flows.recs[f.taskID]
}

func (f *fixture) column() domain.TaskColumn { return f.board.tasks[f.taskID].Column }

// moveTo is a board move made outside the flow (an agent, a human).
func (f *fixture) moveTo(t *testing.T, column domain.TaskColumn) {
	t.Helper()
	task := f.board.tasks[f.taskID]
	task.Column = column
	f.board.tasks[f.taskID] = task
}

// hold is what repository.Service does when a reviewer passes the task.
func (f *fixture) hold(t *testing.T) bool {
	t.Helper()
	return f.svc.HoldReviewPromotion(context.Background(), f.board.tasks[f.taskID],
		domain.TaskColumnCodeReview, domain.TaskColumnReadyForQA, domain.TaskActorAgent)
}

func TestReviewPassIsHeldUntilTheIntegrationDeployIsGreen(t *testing.T) {
	f := newFixture(t)

	if !f.hold(t) {
		t.Fatal("the flow must hold a passing review")
	}
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatalf("the card must not move yet: %s", f.column())
	}

	rec := f.sweep(t)
	if rec.Status != domain.IntegrationWaiting || f.gh.created != 1 || f.gh.mergeCalls != 0 {
		t.Fatalf("first pass opens the PR and waits for mergeability: %+v created=%d", rec, f.gh.created)
	}
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatalf("still held: %s", f.column())
	}

	rec = f.sweep(t)
	if rec.Status != domain.IntegrationMerged || rec.MergeSHA != "m-h1" || rec.DeployStatus != domain.IntegrationDeployPending {
		t.Fatalf("second pass merges into development: %+v", rec)
	}
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatalf("a running deploy keeps the card in review: %s", f.column())
	}

	f.gh.runs = []port.ActionsRun{{Status: "in_progress", HTMLURL: "run1"}}
	f.sweep(t)
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatalf("still deploying: %s", f.column())
	}

	f.gh.runs = []port.ActionsRun{{Status: "completed", Conclusion: "success", HTMLURL: "run1"}}
	rec = f.sweep(t)
	if f.column() != domain.TaskColumnReadyForQA {
		t.Fatalf("a green deploy hands the task to QA: %s", f.column())
	}
	if rec.PromoteTo != "" {
		t.Fatalf("the hold must be cleared: %+v", rec)
	}
	if f.gh.mergeCalls != 1 {
		t.Fatal("a merged head is never merged again")
	}
	joined := strings.Join(f.board.comments, "\n")
	for _, want := range []string{"Merged into `development`", "deploy for m-h1 succeeded"} {
		if !strings.Contains(joined, want) {
			t.Errorf("comments lack %q:\n%s", want, joined)
		}
	}
}

func TestSystemMovesAreNotHeld(t *testing.T) {
	f := newFixture(t)
	if f.svc.HoldReviewPromotion(context.Background(), f.board.tasks[f.taskID],
		domain.TaskColumnCodeReview, domain.TaskColumnReadyForQA, domain.TaskActorSystem) {
		t.Fatal("the flow's own move must not be held")
	}
	for _, move := range [][2]domain.TaskColumn{
		{domain.TaskColumnCodeReview, domain.TaskColumnNeedRevision},
		{domain.TaskColumnInQA, domain.TaskColumnHumanUAT},
		{domain.TaskColumnTodo, domain.TaskColumnInProgress},
	} {
		if f.svc.HoldReviewPromotion(context.Background(), f.board.tasks[f.taskID], move[0], move[1], domain.TaskActorAgent) {
			t.Errorf("%s → %s must not be held", move[0], move[1])
		}
	}
	delete(f.flows.flows, f.repoID)
	if f.hold(t) {
		t.Error("a repository without a flow is never held")
	}
}

func TestDoneMergesToTheReleaseBranchAndReleases(t *testing.T) {
	f := newFixture(t)
	f.moveTo(t, domain.TaskColumnDone)

	f.sweep(t)
	if f.merger.calls != 1 {
		t.Fatalf("done must merge the task's pull request: %d", f.merger.calls)
	}
	if f.column() != domain.TaskColumnDone {
		t.Fatalf("the card waits for the deploy: %s", f.column())
	}
	task := f.board.tasks[f.taskID]
	task.MergeCommitSHA = "mainsha"
	f.board.tasks[f.taskID] = task

	f.gh.runs = []port.ActionsRun{{Status: "in_progress", HTMLURL: "prod1"}}
	f.sweep(t)
	if f.column() != domain.TaskColumnDone {
		t.Fatalf("a running production deploy keeps it in done: %s", f.column())
	}
	if f.gh.lastRunBranch != "master" {
		t.Fatalf("the release deploy is watched on the release branch, got %q", f.gh.lastRunBranch)
	}

	f.gh.runs = []port.ActionsRun{{Status: "completed", Conclusion: "success", HTMLURL: "prod1"}}
	f.sweep(t)
	if f.column() != domain.TaskColumnReleased {
		t.Fatalf("a green production deploy releases the task: %s", f.column())
	}
	if f.merger.calls != 1 {
		t.Fatal("the merge must not be attempted twice")
	}
}

func TestAFailedReleaseDeployKeepsTheTaskInDone(t *testing.T) {
	f := newFixture(t)
	f.moveTo(t, domain.TaskColumnDone)
	f.sweep(t)
	task := f.board.tasks[f.taskID]
	task.MergeCommitSHA = "mainsha"
	f.board.tasks[f.taskID] = task
	f.gh.runs = []port.ActionsRun{{Status: "completed", Conclusion: "failure", HTMLURL: "prod-bad"}}
	f.sweep(t)
	if f.column() != domain.TaskColumnDone {
		t.Fatalf("column %s", f.column())
	}
	if !strings.Contains(strings.Join(f.board.comments, "\n"), "deploy for mainsha FAILED") {
		t.Errorf("comments: %v", f.board.comments)
	}
}

func TestARefusedReleaseMergeIsReportedOnce(t *testing.T) {
	f := newFixture(t)
	f.moveTo(t, domain.TaskColumnDone)
	f.merger.err = errors.New("GitHub reports pull request #1 as `blocked`")
	f.sweep(t)
	f.sweep(t)
	if f.column() != domain.TaskColumnDone {
		t.Fatalf("column %s", f.column())
	}
	var refusals int
	for _, c := range f.board.comments {
		if strings.Contains(c, "was refused") {
			refusals++
		}
	}
	if refusals != 1 {
		t.Fatalf("one refusal comment, got %d: %v", refusals, f.board.comments)
	}
}

func TestReleaseBranchForWorkspace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if got := f.svc.ReleaseBranchForWorkspace(ctx, "/ws/task-"+f.taskID.String()); got != "master" {
		t.Errorf("task workspace: %q", got)
	}
	if got := f.svc.ReleaseBranchForWorkspace(ctx, "/ws/repos/app"); got != "" {
		t.Errorf("a repository clone has no task: %q", got)
	}
	if got := f.svc.ReleaseBranchForWorkspace(ctx, "/ws/task-"+uuid.NewString()); got != "" {
		t.Errorf("unknown task: %q", got)
	}
	f.flows.flows[f.repoID] = domain.BranchFlow{RepositoryID: f.repoID, IntegrationBranch: "development"}
	if got := f.svc.ReleaseBranchForWorkspace(ctx, "/ws/task-"+f.taskID.String()); got != "" {
		t.Errorf("no release branch configured means the default branch: %q", got)
	}
}

func TestConflictSendsTheTaskBack(t *testing.T) {
	f := newFixture(t)
	f.gh.nextState = "dirty"
	f.hold(t)
	f.sweep(t)
	rec := f.sweep(t)
	if rec.Status != domain.IntegrationConflict || rec.Reason != domain.IntegrationReasonConflict {
		t.Fatalf("status %s reason %s", rec.Status, rec.Reason)
	}
	if f.column() != domain.TaskColumnNeedRevision {
		t.Fatalf("column %s", f.column())
	}
	if !strings.Contains(strings.Join(f.board.comments, ""), "Do not merge `development` into the task branch") {
		t.Errorf("comments: %v", f.board.comments)
	}
	if f.gh.mergeCalls != 0 {
		t.Error("a conflicting PR is not merged")
	}
}

func TestRevisionIsMergedAgain(t *testing.T) {
	f := newFixture(t)
	f.hold(t)
	f.sweep(t)
	f.sweep(t)
	f.gh.runs = []port.ActionsRun{{Status: "completed", Conclusion: "success"}}
	f.sweep(t)
	if f.column() != domain.TaskColumnReadyForQA {
		t.Fatalf("column %s", f.column())
	}

	// Revision: back to code_review with new commits, held again.
	f.moveTo(t, domain.TaskColumnCodeReview)
	f.gh.head = "h2"
	f.gh.runs = nil
	f.hold(t)
	rec := f.sweep(t)
	if rec.HeadSHA != "h2" || rec.Status == domain.IntegrationMerged {
		t.Fatalf("new head not picked up: %+v", rec)
	}
	rec = f.sweep(t)
	if rec.Status != domain.IntegrationMerged || rec.MergeSHA != "m-h2" {
		t.Fatalf("new head merged: %+v", rec)
	}
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatalf("still waiting for the new deploy: %s", f.column())
	}
	f.gh.runs = []port.ActionsRun{{Status: "completed", Conclusion: "success"}}
	f.sweep(t)
	if f.column() != domain.TaskColumnReadyForQA {
		t.Fatalf("column %s", f.column())
	}
}

func TestAFailedIntegrationDeployKeepsTheTaskInReview(t *testing.T) {
	f := newFixture(t)
	f.hold(t)
	f.sweep(t)
	f.sweep(t)
	f.gh.runs = []port.ActionsRun{
		{Status: "completed", Conclusion: "success", HTMLURL: "ok"},
		{Status: "completed", Conclusion: "failure", HTMLURL: "bad"},
	}
	rec := f.sweep(t)
	if rec.DeployStatus != domain.IntegrationDeployFailure || rec.DeployURL != "bad" {
		t.Fatalf("%+v", rec)
	}
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatalf("a failed deploy does not hand the task to QA: %s", f.column())
	}
	if !strings.Contains(strings.Join(f.board.comments, "\n"), "FAILED") {
		t.Errorf("comments: %v", f.board.comments)
	}
}

func TestNoWorkflowMeansNoDeploy(t *testing.T) {
	f := newFixture(t)
	f.hold(t)
	f.sweep(t)
	f.sweep(t)
	if rec := f.sweep(t); rec.DeployStatus != domain.IntegrationDeployPending {
		t.Fatalf("right after the merge: %+v", rec)
	}
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatalf("column %s", f.column())
	}
	f.now = f.now.Add(noRunGrace)
	if rec := f.sweep(t); rec.DeployStatus != domain.IntegrationDeployNone {
		t.Fatalf("after the grace period: %+v", rec)
	}
	if f.column() != domain.TaskColumnReadyForQA {
		t.Fatalf("a branch with no deploy still reaches QA: %s", f.column())
	}
}

func TestOnlyTheFlowsOwnColumnsAreTouched(t *testing.T) {
	f := newFixture(t)
	f.hold(t)
	f.moveTo(t, domain.TaskColumnInQA)
	f.sweep(t)
	if f.gh.created != 0 || f.merger.calls != 0 {
		t.Fatal("a task between the two stages was touched")
	}
	f.moveTo(t, domain.TaskColumnCodeReview)
	delete(f.flows.flows, f.repoID)
	f.sweep(t)
	if f.gh.created != 0 {
		t.Fatal("a repository without a flow was touched")
	}
}

func TestMissingTokenIsReportedOnce(t *testing.T) {
	f := newFixture(t)
	f.svc.tokens = func(context.Context) (string, error) { return "", nil }
	f.hold(t)
	f.sweep(t)
	f.sweep(t)
	rec := f.flows.recs[f.taskID]
	if rec.Status != domain.IntegrationFailed || rec.Reason != domain.IntegrationReasonNoToken ||
		!strings.Contains(rec.Detail, "GitHub is not connected") {
		t.Fatalf("%+v", rec)
	}
	if len(f.board.comments) != 1 {
		t.Fatalf("comments: %v", f.board.comments)
	}
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatalf("column %s", f.column())
	}
}

func TestSetFlowValidatesTheBranches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, bad := range []string{"has space", "-x", "a..b", "a/", "x.lock", "a;rm"} {
		if _, err := f.svc.SetFlow(ctx, f.repoID, bad, "master"); err == nil {
			t.Errorf("integration %q accepted", bad)
		}
		if _, err := f.svc.SetFlow(ctx, f.repoID, "development", bad); err == nil {
			t.Errorf("release %q accepted", bad)
		}
	}
	if _, err := f.svc.SetFlow(ctx, f.repoID, "development", "development"); err == nil {
		t.Error("the same branch for both was accepted")
	}
	fl, err := f.svc.SetFlow(ctx, f.repoID, " release/dev ", " master ")
	if err != nil || fl.IntegrationBranch != "release/dev" || fl.ReleaseBranch != "master" {
		t.Fatalf("%+v %v", fl, err)
	}
	if fl, err := f.svc.SetFlow(ctx, f.repoID, "development", ""); err != nil || fl.ReleaseBranch != "" {
		t.Fatalf("an empty release branch means the default branch: %+v %v", fl, err)
	}
	if _, err := f.svc.SetFlow(ctx, f.repoID, "", ""); err != nil || f.svc.Enabled(ctx, f.repoID) {
		t.Fatalf("clearing the integration branch turns the flow off: %v", err)
	}
}

func TestBlockedPRWaits(t *testing.T) {
	f := newFixture(t)
	f.gh.nextState = "blocked"
	f.hold(t)
	f.sweep(t)
	rec := f.sweep(t)
	if rec.Status != domain.IntegrationWaiting || rec.Reason != domain.IntegrationReasonChecksPending ||
		!strings.Contains(rec.Detail, "`blocked`") || f.gh.mergeCalls != 0 {
		t.Fatalf("%+v", rec)
	}
	if f.column() != domain.TaskColumnCodeReview {
		t.Fatal("waiting does not move the task")
	}
}
