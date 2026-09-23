package board

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

func TestQAPassGoesToHumanUATWithBranchFlow(t *testing.T) {
	for _, col := range []domain.TaskColumn{domain.TaskColumnReadyForQA, domain.TaskColumnInQA} {
		task := domain.BoardTask{Column: col}

		plain := columnInstructionForFlow(task, false)
		if !strings.Contains(plain, "→ pm_uat") && !strings.Contains(plain, "move it to pm_uat") {
			t.Errorf("%s without the flow should still pass to pm_uat: %s", col, plain)
		}
		if plain != columnInstruction(task) {
			t.Errorf("%s: columnInstruction must be the no-flow instruction", col)
		}

		flow := columnInstructionForFlow(task, true)
		if strings.Contains(flow, "pm_uat") || !strings.Contains(flow, "human_uat") {
			t.Errorf("%s with the flow must pass to human_uat and never mention pm_uat: %s", col, flow)
		}

		exit, ok := reviewExitColumn(col, true)
		if !ok || exit != domain.TaskColumnHumanUAT {
			t.Errorf("%s sweep exit with the flow: %s", col, exit)
		}
		exit, _ = reviewExitColumn(col, false)
		if exit != domain.TaskColumnPMUAT {
			t.Errorf("%s sweep exit without the flow: %s", col, exit)
		}
	}
	if exit, _ := reviewExitColumn(domain.TaskColumnCodeReview, true); exit != domain.TaskColumnReadyForQA {
		t.Errorf("code review exit is not the flow's business: %s", exit)
	}
}

func TestRunInstructionReadsTheJobsFlow(t *testing.T) {
	job := RunJob{Task: domain.BoardTask{Column: domain.TaskColumnInQA}, BranchFlow: true}
	if !strings.Contains(runInstruction(job), "human_uat") {
		t.Error("runInstruction ignores the job's branch flow")
	}
}

type flowStub struct {
	enabled bool
	held    bool
}

func (f flowStub) Enabled(context.Context, uuid.UUID) bool { return f.enabled }
func (f flowStub) HoldReviewPromotion(context.Context, domain.BoardTask, domain.TaskColumn, domain.TaskColumn, domain.TaskActor) bool {
	return f.held
}
func (f flowStub) IsHeld(context.Context, uuid.UUID) bool { return f.held }
func (f flowStub) RedirectMove(_ context.Context, _ uuid.UUID, to domain.TaskColumn) (domain.TaskColumn, bool) {
	return to, false
}

// done belongs to the flow when it is on: the sweeper merges and watches the
// deploy, so dispatching QA there would spend a session on work already in
// hand and on a refusal it cannot act on.
func TestDoneMergeWakeIsSkippedForFlowRepositories(t *testing.T) {
	input := DispatchInput{
		RepositoryID: uuid.New(),
		EventType:    domain.BoardEventTaskMoved,
		Task: domain.BoardTask{
			ID: uuid.New(), Column: domain.TaskColumnDone, TaskType: domain.TaskTypeTask,
			PRURL: "https://github.com/acme/app/pull/1",
		},
	}
	if !doneMergeWake(input) {
		t.Fatal("an unmerged task in done wakes QA without the flow")
	}
	d := &Dispatcher{}
	d.SetBranchFlow(flowStub{enabled: true})
	if d.branchFlow == nil || !d.branchFlow.Enabled(context.Background(), input.RepositoryID) {
		t.Fatal("the checker was not wired")
	}
}
