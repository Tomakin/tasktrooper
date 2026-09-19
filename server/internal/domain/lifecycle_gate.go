package domain

import "errors"

// Lifecycle-gate errors.
//
// The board's two terminal columns are claims about the world, and until these
// gates existed nothing checked either one:
//
//   - done claims "this passed its review chain". A human (or an agent) could
//     drag a card from in_progress straight into done and the control plane
//     recorded a reviewed, QA'd, UAT-approved task that no reviewer, no QA
//     round and no PM had ever seen.
//   - released claims "this is live in production". Nothing tied it to a deploy,
//     so a task could be marked shipped while its code sat on an unmerged
//     branch.
//
// Both fail closed on unreadable evidence, for the same reason releaseTargetGate
// does: a check that passes when its input is missing is not a check.
var (
	// ErrReviewChainIncomplete blocks done/released for a task that never
	// passed one of the stages its type requires. The wrapped detail names the
	// missing stages and the move that earns each one.
	ErrReviewChainIncomplete = errors.New("done means the task passed its review chain, and this one has not")
	// ErrReviewStageRejected blocks done/released for a task whose most recent
	// visit to a review stage ended in a recorded rejection. Having visited a
	// gate is not the same as having passed it.
	ErrReviewStageRejected = errors.New("a review stage rejected this task and it has not been re-reviewed since")
	// ErrReleaseNotDeployed blocks released for a task with no successful
	// production deploy recorded against it.
	ErrReleaseNotDeployed = errors.New("released means the task is live in production, and no successful production deploy is recorded for it")
)

// ReviewStage is one mandatory step of a task's review chain.
//
// Column is the evidence: task_column_spans records every visit a task makes to
// every column, so "has this task ever been in code_review" answers "was it
// reviewed" without depending on who moved it or on a verdict field that is
// only written when require_human_review is on. A task that bounced through
// need_revision and came back keeps its earlier spans, so rework is not
// punished — the stage stays passed.
type ReviewStage struct {
	// Column must appear in the task's span history for the stage to count.
	Column TaskColumn
	// Label is what a block message calls this stage to a human.
	Label string
	// Remedy is the move that earns the stage, named in the block message so
	// the error is actionable rather than merely correct.
	Remedy string
}

// ReviewChainForType returns the stages a task of this type must have passed
// before it may enter done (or released).
//
// The chains are derived from how the board is actually wired, not from an
// idealised lifecycle:
//
//	task / bug — code_review (system-architect reviews the diff), in_qa (the
//	  column the QA agent tests in; ready_for_qa is only its inbox), pm_uat
//	  (product-manager verifies each acceptance criterion against QA's evidence).
//
//	analiz — analiz_review only. It produces a spec and a plan, there is nothing
//	  to test or accept; the human approving in analiz_review IS the review.
//
// human_uat is deliberately NOT required. It is a human approval gate with no
// subscriber, and whether a task passes through it depends on a repo setting:
// with require_human_review off the PM moves pm_uat → human_uat, but with it on
// the PM's move is intercepted and held, so the human moves the task out of
// pm_uat directly — often straight to done. Requiring human_uat would strand
// every task on a repo using that setting.
//
// An unrecognised (or empty, i.e. pre-typing) task type is treated as coding
// work: that is what CreateTask defaults to, and it is the fail-closed
// direction.
func ReviewChainForType(t TaskType) []ReviewStage {
	if t == TaskTypeAnaliz {
		return []ReviewStage{{
			Column: TaskColumnAnalizReview,
			Label:  "analiz review",
			Remedy: "move it to analiz_review and approve the spec/plan there",
		}}
	}
	return []ReviewStage{
		{
			Column: TaskColumnCodeReview,
			Label:  "code review",
			Remedy: "move it to code_review so the diff is reviewed",
		},
		{
			Column: TaskColumnInQA,
			Label:  "QA",
			Remedy: "move it to ready_for_qa; QA takes it into in_qa and tests it there",
		},
		{
			Column: TaskColumnPMUAT,
			Label:  "UAT",
			Remedy: "move it to pm_uat so every acceptance criterion is verified against QA's evidence",
		},
	}
}

// ReviewChainForFlow is ReviewChainForType for a repository that runs the
// two-stage delivery (development → main). There is no PM UAT in that flow:
// the task is put on the integration branch and a human tests it there, so the
// acceptance stage is human_uat.
func ReviewChainForFlow(t TaskType, branchFlow bool) []ReviewStage {
	stages := ReviewChainForType(t)
	if !branchFlow {
		return stages
	}
	for i, stage := range stages {
		if stage.Column == TaskColumnPMUAT {
			stages[i] = ReviewStage{
				Column: TaskColumnHumanUAT,
				Label:  "human UAT",
				Remedy: "move it to human_uat so it is merged into the integration branch and a human tests it there",
			}
		}
	}
	return stages
}

// TaskTypeShipsCode reports whether a task of this type is expected to reach
// production through a deploy. An analiz task ships nothing: its own workflow
// moves it done → released once the implementation tasks it produced have been
// created, so gating that move on a deploy would park every analysis forever.
func TaskTypeShipsCode(t TaskType) bool {
	return t != TaskTypeAnaliz
}
