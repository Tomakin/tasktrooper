package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/makifbaysal/tasktrooper/server/internal/application/board"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

// StageEvidence is the slice of port.TaskColumnSpanStore the lifecycle gates
// read. Narrow on purpose, the way board.RevisionLookup and board.VerdictStore
// are: the gate needs the span ledger's history, not its writers.
type StageEvidence interface {
	LatestVerdicts(ctx context.Context, taskID uuid.UUID) (map[string]string, error)
}

// SetSpanStore wires the column-span ledger the review-chain gate reads its
// evidence from. Nil leaves the gate unable to prove anything, which — on a
// repository that armed it — is a block, not a pass.
func (s *Service) SetSpanStore(spans StageEvidence) {
	s.spans = spans
}

// SetLifecycleGates arms or disarms a repository's stage gates: the review
// chain before done, the production deploy before released, whether code_review
// waits for CI before its reviewer is dispatched, and — the one pair that gates
// nothing — how the repository's WHOLE-REPO coverage figure is reported, and
// against what number. Each toggle is applied only when its pointer is non-nil.
//
// coverageThreshold is validated rather than trusted: it is a percentage, and a
// negative or above-100 bar is a typo that would put a nonsense number in every
// run's hand-off. 0 is allowed and means "use the default".
func (s *Service) SetLifecycleGates(ctx context.Context, repositoryID uuid.UUID, requireReviewChain, requireReleaseDeploy, requirePipelineForReview, requireOverallCoverage *bool, coverageThreshold *float64) (domain.Repository, error) {
	if s.repos == nil {
		return domain.Repository{}, fmt.Errorf("repository store unavailable")
	}
	if coverageThreshold != nil && (*coverageThreshold < 0 || *coverageThreshold > 100) {
		return domain.Repository{}, fmt.Errorf(
			"coverage_threshold must be between 0 and 100 (0 means the default %.0f%%), got %.1f",
			board.DefaultCoverageThreshold, *coverageThreshold)
	}
	return s.repos.UpdateLifecycleGates(ctx, repositoryID, requireReviewChain, requireReleaseDeploy, requirePipelineForReview, requireOverallCoverage, coverageThreshold)
}

// RequirePipelineForReview answers the board dispatcher's question: does this
// repository hold its code review back until CI reports?
//
// It fails CLOSED — an unreadable repository still gates — and that is the
// opposite of what "fail closed" usually buys, so it is worth stating why.
// Holding the gate is recoverable: PipelineGateSweeper opens it within the
// window whatever the reason, so the worst case of a wrong `true` is a card
// that waits. Dispatching a reviewer is not recoverable: a run has started, a
// model has been paid for, and a review has been recorded against a diff
// nobody built. So the error case takes the side that can be undone by time.
//
// It reads the repository on every gated move, which is one indexed primary-key
// lookup on a path that is already writing a board event and a run row.
func (s *Service) RequirePipelineForReview(ctx context.Context, repositoryID uuid.UUID) bool {
	if s.repos == nil {
		return true
	}
	repo, err := s.repos.Get(ctx, repositoryID)
	if err != nil {
		log.Warn().Err(err).Str("repository_id", repositoryID.String()).
			Msg("pipeline gate: repository unreadable, keeping the review gate closed")
		return true
	}
	return repo.RequirePipelineForReview
}

// reviewChainGate refuses to let a task enter done (or released, which is done's
// successor and must not become a way around it) before it has passed every
// review stage its type requires.
//
// Evidence is the span ledger, not the current column: task_column_spans keeps
// every visit a task ever made to every column, so a task that was rejected
// into need_revision and came back still counts as having passed the stages it
// re-entered. Punishing rework would make the gate an argument for hiding it.
//
// Where a verdict WAS recorded (only while require_human_review is on) it is
// used as a second, sharper signal: if the task's most recent visit to a stage
// ended in a recorded rejection, it visited that gate but did not pass it, and
// dragging it forward from need_revision is refused. A later clean visit
// overwrites that — which is exactly the rework loop, working.
//
// Applies to humans and agents alike. The owner's rule is about what done
// means, and a state that means different things depending on who typed the
// move means nothing.
func (s *Service) reviewChainGate(ctx context.Context, repo domain.Repository, task domain.BoardTask, prev, target domain.TaskColumn) error {
	if !repo.RequireReviewChain {
		return nil
	}
	if target != domain.TaskColumnDone && target != domain.TaskColumnReleased {
		return nil
	}
	// released is gated so that skipping done is not a way around the chain —
	// but the ordinary done → released promotion is not re-checked. done
	// already asserted the chain, and re-asserting it here would retro-block
	// every task already sitting in done on the day a repository opts in,
	// including the ones the pipeline runner is about to release for real.
	if target == domain.TaskColumnReleased && prev == domain.TaskColumnDone {
		return nil
	}
	stages := domain.ReviewChainForFlow(task.TaskType, s.branchFlowEnabled(ctx, repo.ID))
	if len(stages) == 0 {
		return nil
	}
	if s.spans == nil {
		return fmt.Errorf("%w: the column-span ledger is not available, so its review history cannot be read. "+
			"Fix the control plane's span store, or turn require_review_chain off for this repository", domain.ErrReviewChainIncomplete)
	}
	verdicts, err := s.spans.LatestVerdicts(ctx, task.ID)
	if err != nil {
		// Fail closed. An unreadable history is the case the gate exists for:
		// treating "cannot check" as "checked and fine" is how an unreviewed
		// task reaches done during a database hiccup.
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("review-chain gate could not read span history")
		return fmt.Errorf("%w: its stage history could not be read (%v) — retry the move", domain.ErrReviewChainIncomplete, err)
	}

	var missing, rejected []string
	for _, stage := range stages {
		// A board that does not have this column cannot route a task through
		// it, so requiring it would be a deadlock rather than a check.
		// board_columns is user-editable and there is an open product todo
		// about editing the wiring from the UI, so this is a live case, not a
		// hypothetical.
		if !s.boardHasColumn(ctx, stage.Column) {
			continue
		}
		verdict, visited := verdicts[string(stage.Column)]
		switch {
		case !visited:
			missing = append(missing, fmt.Sprintf("%s (%s) — %s", stage.Label, stage.Column, stage.Remedy))
		case verdict == domain.ReviewVerdictReject:
			rejected = append(rejected, fmt.Sprintf("%s (%s) — %s", stage.Label, stage.Column, stage.Remedy))
		}
	}

	// Rejections first: "you were sent back and never came back" is a more
	// specific and more useful message than "a stage is missing".
	if len(rejected) > 0 {
		return fmt.Errorf("%w — cannot move %s to %s. Rejected at: %s",
			domain.ErrReviewStageRejected, taskLabel(task), target, strings.Join(rejected, "; "))
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w — cannot move %s to %s. Missing: %s",
			domain.ErrReviewChainIncomplete, taskLabel(task), target, strings.Join(missing, "; "))
	}
	return nil
}

// CheckReviewChain answers "may this task be in done at all?" for a caller
// outside the move path — today the pull-request merge, which is irreversible
// and therefore re-asks the gate instead of trusting the column.
//
// It is the SAME reviewChainGate the move into done runs, called with done as
// the target, so there is exactly one definition of a complete review chain. A
// second implementation for the merge is the failure mode this method exists to
// prevent: two nearly-identical chain checks that agree until the day someone
// edits one of them.
//
// A repository with require_review_chain off returns nil, exactly as the move
// does — the flag is the owner's assertion that their board can clear it.
func (s *Service) CheckReviewChain(ctx context.Context, repositoryID, taskID uuid.UUID) error {
	if s.repos == nil || s.tasks == nil {
		return fmt.Errorf("repository store unavailable")
	}
	repo, err := s.repos.Get(ctx, repositoryID)
	if err != nil {
		return err
	}
	if !repo.RequireReviewChain {
		return nil
	}
	task, err := s.tasks.Get(ctx, repositoryID, taskID)
	if err != nil {
		return err
	}
	// prev is the task's own column rather than a guess: the gate only reads it
	// to let the ordinary done → released promotion through, which is not what
	// a target of done can be.
	return s.reviewChainGate(ctx, repo, task, task.Column, domain.TaskColumnDone)
}

// releaseDeployGate refuses to let a task enter released before a production
// deploy has actually succeeded for it.
//
// What "a successful production deploy for this task" is in task_pipelines,
// read off the pipeline runner rather than assumed:
//
//   - trigger prod_deploy, status success. This is the move the runner itself
//     makes on the back of one (finalize → moveTask(released)), so the gate and
//     the automation agree by construction.
//   - trigger preprod_deploy, status success, on a repository with NO prod
//     deploy workflow mapped. That is the runner's own fallback — "preprod is
//     the highest enabled env, so it releases the task" — and refusing it would
//     block the release the runner just performed. On a repo that DOES map
//     prod, a preprod success proves nothing about production and is ignored.
//
// Status skipped is deliberately not accepted. It means no workflow was mapped
// and nothing executed; the codebase already treats it as "passes the gate, but
// is not evidence" everywhere it matters (the stage-verification stamp and the
// store submit both require a real successful job). A repository in that state
// must leave require_release_deploy off — the block message says so.
//
// analiz tasks are exempt: they ship no code, and their own workflow moves them
// done → released once the implementation tasks have been created.
func (s *Service) releaseDeployGate(ctx context.Context, repo domain.Repository, task domain.BoardTask, target domain.TaskColumn) error {
	if !repo.RequireReleaseDeploy || target != domain.TaskColumnReleased {
		return nil
	}
	if !domain.TaskTypeShipsCode(task.TaskType) {
		return nil
	}
	if s.pipelineStore == nil {
		return fmt.Errorf("%w: the pipeline ledger is not available, so no deploy can be proven. "+
			"Fix the control plane's pipeline store, or turn require_release_deploy off for this repository",
			domain.ErrReleaseNotDeployed)
	}
	evidence, err := s.releaseEvidence(ctx, repo.ID, task.ID)
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("release-deploy gate could not read pipelines")
		return fmt.Errorf("%w: its deploy history could not be read (%v) — retry the move", domain.ErrReleaseNotDeployed, err)
	}
	if evidence.Deployed {
		return nil
	}

	remedy := "Release it through the board (trigger_release / the done column's release dispatch) and let the prod deploy finish before moving it here."
	sawSkipped := evidence.SawSkipped
	if sawSkipped {
		remedy = "Its production deploy ran nothing: no prod deploy workflow is mapped for this repository. " +
			"Map one under the repository's pipeline settings, or turn require_release_deploy off — a skipped pipeline is not a deploy."
	}
	return fmt.Errorf("%w — cannot move %s to released. %s", domain.ErrReleaseNotDeployed, taskLabel(task), remedy)
}

// ReleaseEvidence is what task_pipelines can prove about a task reaching
// production. It is the single definition of "this task is live", shared by the
// released-column gate, the deploy-dependency gate and the package advancer —
// three places that must never disagree about what shipped.
type ReleaseEvidence struct {
	// Deployed is the verdict: a successful prod_deploy, or a successful
	// preprod_deploy on a repository with no prod workflow mapped (the runner's
	// own fallback — preprod is then the highest enabled env and it is what
	// releases the task).
	Deployed bool
	// SawSkipped records that a deploy pipeline finished as SKIPPED: nothing was
	// mapped, so nothing ran. Not evidence, but a materially different reason
	// for the block, and the remedy differs.
	SawSkipped bool
}

// releaseEvidence reads a task's deploy history and answers "is this live in
// production". Errors propagate: every caller treats an unreadable history as a
// block, and a silent false-with-nil would be indistinguishable from a proven
// "never deployed".
func (s *Service) releaseEvidence(ctx context.Context, repositoryID, taskID uuid.UUID) (ReleaseEvidence, error) {
	if s.pipelineStore == nil {
		return ReleaseEvidence{}, fmt.Errorf("pipeline ledger unavailable")
	}
	runs, err := s.pipelineStore.ListByTask(ctx, taskID)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	prodMapped := s.deployCategoryMapped(ctx, repositoryID, domain.PipelineCategoryProdDeploy)
	var out ReleaseEvidence
	for _, run := range runs {
		if run.Trigger != domain.PipelineTriggerProdDeploy && run.Trigger != domain.PipelineTriggerPreProdDeploy {
			continue
		}
		if run.Status == domain.PipelineStatusSuccess {
			if run.Trigger == domain.PipelineTriggerProdDeploy || !prodMapped {
				out.Deployed = true
				return out, nil
			}
			continue
		}
		if run.Status == domain.PipelineStatusSkipped {
			out.SawSkipped = true
		}
	}
	return out, nil
}

// taskIsLive answers the deploy-dependency question for ONE task: has it
// reached production, either by proven deploy evidence or by sitting in the
// released column.
//
// The released column counts on purpose. It is the board's own statement that
// the task shipped, it is what require_release_deploy already gates when an
// owner wants it proven, and a repository that releases outside this control
// plane (a manual deploy, an older task from before the pipeline existed) would
// otherwise deadlock every dependent forever. A repository that wants the
// stricter reading turns require_release_deploy on, which makes the column
// itself unreachable without evidence.
func (s *Service) taskIsLive(ctx context.Context, repositoryID uuid.UUID, task domain.BoardTask) (bool, error) {
	if task.Column == domain.TaskColumnReleased {
		return true, nil
	}
	evidence, err := s.releaseEvidence(ctx, repositoryID, task.ID)
	if err != nil {
		return false, err
	}
	return evidence.Deployed, nil
}

// deployDependencyGate refuses to dispatch a release for a task whose
// deploy_depends_on targets have not shipped.
//
// Every other release gate reads the task itself: is it reviewed, is its
// migration staged, is the branch still at the verified commit. None of them
// can see that the API this client calls does not exist in production yet, so a
// task that passes all of them still deploys into an environment where it
// cannot work. This is the only gate that looks at other tasks.
//
// Fails closed on an unreadable dependency, same as its neighbours: "cannot
// check" is the case the gate exists for.
func (s *Service) deployDependencyGate(ctx context.Context, repositoryID uuid.UUID, task domain.BoardTask) error {
	if s.relations == nil {
		return nil
	}
	rels, err := s.relations.ListBySource(ctx, task.ID)
	if err != nil {
		log.Warn().Err(err).Str("task_id", task.ID.String()).Msg("deploy-dependency gate could not read relations")
		return fmt.Errorf("%w: its deploy dependencies could not be read (%v) — retry the release",
			domain.ErrDeployDependencyNotReleased, err)
	}

	var blocking []string
	for _, rel := range rels {
		if rel.RelationType != domain.TaskRelationDeployDependsOn {
			continue
		}
		label := rel.TargetKey
		if strings.TrimSpace(label) == "" {
			label = rel.TargetTaskID.String()
		}
		target, terr := s.tasks.Get(ctx, repositoryID, rel.TargetTaskID)
		if terr != nil {
			// A dependency in another repository (or one that has been deleted)
			// cannot be proven from here. Block: an unprovable dependency is
			// exactly what this gate refuses to wave through.
			blocking = append(blocking, label+" (cannot be read from this repository)")
			continue
		}
		live, lerr := s.taskIsLive(ctx, repositoryID, target)
		if lerr != nil {
			blocking = append(blocking, label+" (its deploy history could not be read)")
			continue
		}
		if !live {
			blocking = append(blocking, label)
		}
	}
	if len(blocking) == 0 {
		return nil
	}

	err = fmt.Errorf("%w — %s must deploy after: %s",
		domain.ErrDeployDependencyNotReleased, taskLabel(task), strings.Join(blocking, ", "))
	if s.comments != nil {
		_, _ = s.comments.Create(ctx, domain.TaskComment{
			TaskID:     task.ID,
			AuthorType: "system",
			Content: "Release blocked: this task declares a deploy dependency that is not live in production yet — " +
				strings.Join(blocking, ", ") + ".\n\n" +
				"Release those first (or drop the dependency if the ordering no longer applies), then release this task.",
		})
	}
	log.Warn().Str("task_id", task.ID.String()).Str("repository_id", repositoryID.String()).
		Strs("blocking", blocking).Msg("release blocked: deploy dependency not released")
	return err
}

// deployCategoryMapped reports whether the repository has a real workflow
// mapped for a deploy category. Same predicate PipelineRunner.deployMapped
// uses, so "prod is configured" means one thing across the codebase.
// Unreadable mappings answer "mapped": on this path that is the strict
// direction (it stops a preprod success from standing in for a prod deploy).
func (s *Service) deployCategoryMapped(ctx context.Context, repositoryID uuid.UUID, category string) bool {
	if s.pipelineJobs == nil {
		return false
	}
	mappings, err := s.pipelineJobs.ListByRepository(ctx, repositoryID)
	if err != nil {
		log.Warn().Err(err).Str("repository_id", repositoryID.String()).Msg("lifecycle gate: pipeline mapping lookup failed")
		return true
	}
	for _, m := range mappings {
		if m.Category == category && m.TargetKind == domain.PipelineTargetWorkflow && strings.TrimSpace(m.TargetRef) != "" {
			return true
		}
	}
	return false
}

// boardHasColumn reports whether this board actually has the column a stage
// requires. Columns are per-install rows in board_columns, so a stage can be
// genuinely absent.
//
// A lookup error is read as "absent" rather than propagated: UpdateTask
// validates the move's target column through this same store a few lines
// earlier, so by the time the gate runs the store has already answered once on
// this request. That makes an error here a missing column, not a sick database.
func (s *Service) boardHasColumn(ctx context.Context, col domain.TaskColumn) bool {
	if s.columns == nil {
		return domain.ValidTaskColumn(col)
	}
	return s.columns.ValidateColumn(ctx, string(col)) == nil
}

// taskLabel names a task the way a human reads it on the board, falling back to
// the id for a row with no key yet.
func taskLabel(task domain.BoardTask) string {
	if strings.TrimSpace(task.Key) != "" {
		return task.Key
	}
	return task.ID.String()
}
