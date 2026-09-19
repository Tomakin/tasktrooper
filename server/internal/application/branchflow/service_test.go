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
	flows map[uuid.UUID]domain.BranchFlow
	recs  map[uuid.UUID]domain.TaskIntegration
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
	head       string
	nextState  string
	intPRs     map[int]port.PullRequest
	merged     map[int]string
	runs       []port.ActionsRun
	created    int
	mergeCalls int
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
		flows:  &memFlows{flows: map[uuid.UUID]domain.BranchFlow{}, recs: map[uuid.UUID]domain.TaskIntegration{}},
		board:  &fakeBoard{tasks: map[uuid.UUID]domain.BoardTask{}},
		gh:     newFakeGitHub(),
		merger: &fakeMerger{},
		repoID: uuid.New(),
		taskID: uuid.New(),
		now:    time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC),
	}
	f.flows.flows[f.repoID] = domain.BranchFlow{RepositoryID: f.repoID, IntegrationBranch: "development"}
	f.board.tasks[f.taskID] = domain.BoardTask{ID: f.taskID, RepositoryID: f.repoID, Key: "A-1", Title: "Thing",
		Column: domain.TaskColumnHumanUAT, PRNumber: 1, PRURL: "https://github.com/acme/app/pull/1"}
	f.svc = New(Deps{
		Flows: f.flows, Tasks: f.board, Board: f.board, Merger: f.merger, PRs: f.gh, GitHub: f.gh,
		Tokens:      func(context.Context) (string, error) { return "tok", nil },
		Coordinates: func(context.Context, uuid.UUID) (string, string, error) { return "acme", "app", nil },
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

func TestFullCycleMergeDeployRelease(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	rec := f.sweep(t)
	if rec.Status != domain.IntegrationWaiting || rec.Reason != domain.IntegrationReasonComputing || f.gh.created != 1 || f.gh.mergeCalls != 0 {
		t.Fatalf("first pass opens the PR and waits for mergeability: %+v created=%d", rec, f.gh.created)
	}
	if _, err := f.svc.Release(ctx, f.repoID, f.taskID); !errors.Is(err, domain.ErrReleaseNotReady) {
		t.Fatalf("release before the merge: %v", err)
	}

	rec = f.sweep(t)
	if rec.Status != domain.IntegrationMerged || rec.MergeSHA != "m-h1" || rec.DeployStatus != domain.IntegrationDeployPending || rec.Reason != "" {
		t.Fatalf("second pass merges: %+v", rec)
	}
	if f.gh.created != 1 {
		t.Fatal("the open PR must be reused, not duplicated")
	}

	f.gh.runs = []port.ActionsRun{{Status: "in_progress", HTMLURL: "run1"}}
	if rec = f.sweep(t); rec.DeployStatus != domain.IntegrationDeployPending {
		t.Fatalf("running deploy: %+v", rec)
	}
	if _, err := f.svc.Release(ctx, f.repoID, f.taskID); !errors.Is(err, domain.ErrReleaseNotReady) {
		t.Fatalf("release during the deploy: %v", err)
	}

	f.gh.runs = []port.ActionsRun{{Status: "completed", Conclusion: "success", HTMLURL: "run1"}}
	if rec = f.sweep(t); rec.DeployStatus != domain.IntegrationDeploySuccess || rec.DeployURL != "run1" {
		t.Fatalf("finished deploy: %+v", rec)
	}
	if f.gh.mergeCalls != 1 {
		t.Fatal("a merged head is never merged again")
	}

	res, err := f.svc.Release(ctx, f.repoID, f.taskID)
	if err != nil || res.Merge == nil || res.Merge.MergeCommitSHA != "mainsha" {
		t.Fatalf("release: %+v %v", res, err)
	}
	if f.column() != domain.TaskColumnDone || f.merger.calls != 1 {
		t.Fatalf("column=%s merges=%d", f.column(), f.merger.calls)
	}

	joined := strings.Join(f.board.comments, "\n")
	for _, want := range []string{"Merged into `development`", "deploy for m-h1 succeeded"} {
		if !strings.Contains(joined, want) {
			t.Errorf("comments lack %q:\n%s", want, joined)
		}
	}
	if len(f.board.comments) != 2 {
		t.Errorf("one comment per change, got %d:\n%s", len(f.board.comments), joined)
	}
}

func TestConflictSendsTheTaskBack(t *testing.T) {
	f := newFixture(t)
	f.gh.nextState = "dirty"
	f.sweep(t)
	rec := f.sweep(t)
	if rec.Status != domain.IntegrationConflict || rec.Reason != domain.IntegrationReasonConflict {
		t.Fatalf("status %s", rec.Status)
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
	f.sweep(t)
	f.sweep(t)
	f.gh.runs = []port.ActionsRun{{Status: "completed", Conclusion: "success"}}
	f.sweep(t)

	f.gh.head = "h2"
	if _, err := f.svc.Release(context.Background(), f.repoID, f.taskID); !errors.Is(err, domain.ErrReleaseNotReady) ||
		!strings.Contains(err.Error(), "branch has moved") {
		t.Fatalf("release with unmerged new commits: %v", err)
	}

	f.gh.runs = nil
	rec := f.sweep(t)
	if rec.HeadSHA != "h2" || rec.Status == domain.IntegrationMerged && rec.MergeSHA == "m-h1" {
		t.Fatalf("new head not picked up: %+v", rec)
	}
	rec = f.sweep(t)
	if rec.Status != domain.IntegrationMerged || rec.MergeSHA != "m-h2" || rec.DeployStatus != domain.IntegrationDeployPending {
		t.Fatalf("new head merged: %+v", rec)
	}
}

func TestFailedDeployBlocksRelease(t *testing.T) {
	f := newFixture(t)
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
	_, err := f.svc.Release(context.Background(), f.repoID, f.taskID)
	if !errors.Is(err, domain.ErrReleaseNotReady) || !strings.Contains(err.Error(), "deploy failed") {
		t.Fatalf("release after a failed deploy: %v", err)
	}
	if f.column() != domain.TaskColumnHumanUAT {
		t.Fatal("a refused release does not move the task")
	}
}

func TestNoWorkflowMeansNoDeploy(t *testing.T) {
	f := newFixture(t)
	f.sweep(t)
	f.sweep(t)
	if rec := f.sweep(t); rec.DeployStatus != domain.IntegrationDeployPending {
		t.Fatalf("right after the merge: %+v", rec)
	}
	f.now = f.now.Add(noRunGrace)
	if rec := f.sweep(t); rec.DeployStatus != domain.IntegrationDeployNone {
		t.Fatalf("after the grace period: %+v", rec)
	}
	if _, err := f.svc.Release(context.Background(), f.repoID, f.taskID); err != nil {
		t.Fatalf("a branch with no deploy can be released: %v", err)
	}
}

func TestReleaseKeepsTheApprovalWhenTheMergeIsRefused(t *testing.T) {
	f := newFixture(t)
	f.sweep(t)
	f.sweep(t)
	f.gh.runs = []port.ActionsRun{{Status: "completed", Conclusion: "success"}}
	f.sweep(t)
	f.merger.err = errors.New("GitHub reports pull request #1 as `blocked`")
	res, err := f.svc.Release(context.Background(), f.repoID, f.taskID)
	if err != nil || res.MergeError == "" || res.Merge != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if f.column() != domain.TaskColumnDone {
		t.Fatalf("column %s", f.column())
	}
	if !strings.Contains(f.board.comments[len(f.board.comments)-1], "merging into the default branch was refused") {
		t.Errorf("comments: %v", f.board.comments)
	}
}

func TestOnlyHumanUATTasksOfFlowReposAreTouched(t *testing.T) {
	f := newFixture(t)
	task := f.board.tasks[f.taskID]
	task.Column = domain.TaskColumnInQA
	f.board.tasks[f.taskID] = task
	f.sweep(t)
	if len(f.flows.recs) != 0 || f.gh.created != 0 {
		t.Fatal("a task outside human_uat was touched")
	}
	delete(f.flows.flows, f.repoID)
	task.Column = domain.TaskColumnHumanUAT
	f.board.tasks[f.taskID] = task
	f.sweep(t)
	if len(f.flows.recs) != 0 {
		t.Fatal("a repository without a flow was touched")
	}
	if _, err := f.svc.Release(context.Background(), f.repoID, f.taskID); !errors.Is(err, domain.ErrBranchFlowNotFound) {
		t.Fatalf("release without a flow: %v", err)
	}
}

func TestMissingTokenIsReportedOnce(t *testing.T) {
	f := newFixture(t)
	f.svc.tokens = func(context.Context) (string, error) { return "", nil }
	f.sweep(t)
	f.sweep(t)
	rec := f.flows.recs[f.taskID]
	if rec.Status != domain.IntegrationFailed || rec.Reason != domain.IntegrationReasonNoToken || !strings.Contains(rec.Detail, "GitHub is not connected") {
		t.Fatalf("%+v", rec)
	}
	if len(f.board.comments) != 1 {
		t.Fatalf("comments: %v", f.board.comments)
	}
}

func TestSetFlowValidatesTheBranch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, bad := range []string{"has space", "-x", "a..b", "a/", "x.lock", "a;rm"} {
		if _, err := f.svc.SetFlow(ctx, f.repoID, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if fl, err := f.svc.SetFlow(ctx, f.repoID, " release/dev "); err != nil || fl.IntegrationBranch != "release/dev" {
		t.Fatalf("%+v %v", fl, err)
	}
	if _, err := f.svc.SetFlow(ctx, f.repoID, ""); err != nil || f.svc.Enabled(ctx, f.repoID) {
		t.Fatalf("clearing the branch turns the flow off: %v", err)
	}
}

func TestBlockedPRWaits(t *testing.T) {
	f := newFixture(t)
	f.gh.nextState = "blocked"
	f.sweep(t)
	rec := f.sweep(t)
	if rec.Status != domain.IntegrationWaiting || rec.Reason != domain.IntegrationReasonChecksPending ||
		!strings.Contains(rec.Detail, "`blocked`") || f.gh.mergeCalls != 0 {
		t.Fatalf("%+v", rec)
	}
	if f.column() != domain.TaskColumnHumanUAT {
		t.Fatal("waiting does not move the task")
	}
}
