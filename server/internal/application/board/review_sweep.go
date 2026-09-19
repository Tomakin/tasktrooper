package board

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/makifbaysal/tasktrooper/server/internal/application/activity"
	"github.com/makifbaysal/tasktrooper/server/internal/application/agent"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

// reviewExitColumn is the column an approving verdict sends the task to, per
// column whose work is a verdict. analiz_review is absent on purpose: it is a
// human approval gate, so there is no agent verdict to sweep there.
func reviewExitColumn(column domain.TaskColumn, branchFlow bool) (domain.TaskColumn, bool) {
	switch column {
	case domain.TaskColumnCodeReview:
		return domain.TaskColumnReadyForQA, true
	case domain.TaskColumnInQA, domain.TaskColumnReadyForQA:
		return domain.TaskColumn(qaPassColumn(branchFlow)), true
	case domain.TaskColumnPMUAT:
		return domain.TaskColumnHumanUAT, true
	default:
		return "", false
	}
}

// criterionReviewRole names whose verdict the criteria gate demands before a
// task may leave this column. It mirrors Service.criteriaReviewGate — the two
// disagreeing is what produces a sweep that asks for the wrong role's verdict
// and a move that is refused anyway.
func criterionReviewRole(column domain.TaskColumn) (domain.CriterionReviewRole, bool) {
	switch column {
	case domain.TaskColumnInQA, domain.TaskColumnReadyForQA:
		return domain.CriterionReviewRoleQA, true
	case domain.TaskColumnPMUAT:
		return domain.CriterionReviewRolePM, true
	default:
		return "", false
	}
}

// missingVerdicts lists the criteria this column's reviewer has not ruled on.
// These are what the criteria gate refuses the forward move over, so they are
// swept before the move rather than after it fails.
func (r *Runner) missingVerdicts(ctx context.Context, job RunJob, role domain.CriterionReviewRole) []domain.AcceptanceCriterion {
	reader, ok := r.taskUpdater.(taskCriteriaReader)
	if !ok {
		return nil
	}
	items, err := reader.ListTaskCriteria(ctx, job.Task.ID)
	if err != nil {
		log.Warn().Err(err).Str("task_id", job.Task.ID.String()).Msg("review sweep: acceptance criteria unreadable")
		return nil
	}
	var missing []domain.AcceptanceCriterion
	for _, c := range items {
		ruled := false
		for _, check := range c.Checks {
			if check.Role == role {
				ruled = true
				break
			}
		}
		if !ruled {
			missing = append(missing, c)
		}
	}
	return missing
}

// stuckVerdictNote names the criteria still missing this column's verdict, so
// the human reading the card knows WHY the move never went through instead of
// re-triggering an agent that will hit the same gate.
func (r *Runner) stuckVerdictNote(ctx context.Context, job RunJob) string {
	role, ok := criterionReviewRole(job.Task.Column)
	if !ok {
		return ""
	}
	missing := r.missingVerdicts(ctx, job, role)
	if len(missing) == 0 {
		return ""
	}
	texts := make([]string, 0, len(missing))
	for _, c := range missing {
		texts = append(texts, c.Text)
	}
	return fmt.Sprintf(" Geçiş kapısı %d kabul kriterini %s verdict'i olmadan geçirmiyor: %s.",
		len(missing), role, strings.Join(texts, "; "))
}

// finalizeReviewVerdict turns a stated verdict into the move the reviewer never
// made, and reports whether the task left the column.
//
// The sweep above it asks the reviewer to call move_board_task. When even that
// produces prose instead of a call — a reviewer that writes "approved, the
// pipeline is green" and stops — the card sat in code_review until a human
// dragged it, with a completed run above it that nothing retries.
//
// This does NOT promote unreviewed work, which is the thing the column exists to
// prevent. It runs only after a real review run, it asks for nothing but the
// verdict that run already reached, and it makes the move as the reviewing agent
// — so every gate a reviewer's own move goes through still applies. Chiefly
// ReviewGate: with require_human_review on, an approval is recorded as a verdict
// and the task is HELD for the human exactly as if the agent had called the tool
// itself. So the automatic advance to ready_for_qa happens only where a human
// approval was never required in the first place.
//
// A verdict that is not one of the two words is not a verdict: the task stays
// put and the stuck-column comment is written, because a reviewer that will not
// say yes or no has not finished reviewing.
//
// The second return value is a usage-limit block from the verdict turn: the
// caller must park the task on it rather than read the bool, which would
// otherwise read as an ordinary "no verdict given".
func (r *Runner) finalizeReviewVerdict(
	ctx context.Context,
	job RunJob,
	agentRec domain.Agent,
	history []domain.Message,
	model string,
	policy domain.ToolPolicy,
	exit domain.TaskColumn,
) (bool, *domain.QuotaBlock) {
	if r.taskUpdater == nil {
		return false, nil
	}
	// Criteria this column's reviewer never ruled on are what the forward gate
	// refuses the move over. The sweep already asked for them; still missing
	// means the move would be refused, and asking for a verdict we cannot act on
	// would only cost a model call.
	if role, ok := criterionReviewRole(job.Task.Column); ok {
		if missing := r.missingVerdicts(ctx, job, role); len(missing) > 0 {
			return false, nil
		}
	}

	ask := "Answer with ONE word and nothing else — no explanation, no tool call.\n" +
		"Based on the review you just completed: `APPROVE` if everything you required is satisfied and the work should move on to `" +
		string(exit) + "`, `REVISE` if anything you flagged still needs work.\n" +
		"This answer is recorded as your verdict and the board move is made from it, so it must match the review you wrote above."
	turn := append(append([]domain.Message{}, history...), domain.Message{Role: domain.RoleUser, Content: ask})

	if rec := activity.FromContext(ctx); rec != nil {
		rec.Step("review_verdict_finalize_start", map[string]any{"column": string(job.Task.Column)})
	}
	resp, err := r.agentLoop.RunTask(ctx, turn, model, agentRec.ProviderType, policy,
		agent.WithLightModel(agentRec.Model),
		agent.WithCLILabel(job.Task.Key+" review-verdict", job.Task.Title))
	if err != nil {
		if quotaErr, ok := domain.QuotaBlockOf(err); ok {
			return false, quotaErr
		}
		log.Warn().Err(err).Str("task_id", job.Task.ID.String()).Msg("review verdict finalize failed; task stays in its review column")
		return false, nil
	}

	target, ok := verdictColumn(resp.Message.Content, exit)
	if !ok {
		log.Info().Str("task_id", job.Task.ID.String()).Msg("review verdict finalize: no verdict in the answer, leaving the column alone")
		return false, nil
	}

	// Attributed to the reviewing agent, like every other hand-off move: the
	// dispatcher skips the agent whose own tool call produced the event, and
	// attributing this to the system would dispatch this same reviewer onto the
	// move it just made.
	agentID := job.Run.AgentID
	if _, err := r.taskUpdater.UpdateTask(ctx, job.RepositoryID, job.Task.ID, domain.UpdateBoardTaskRequest{
		Column:       &target,
		Actor:        domain.TaskActorAgent,
		ActorAgentID: &agentID,
	}); err != nil {
		log.Warn().Err(err).Str("task_id", job.Task.ID.String()).Str("target", string(target)).
			Msg("review verdict finalize: move refused")
		return false, nil
	}

	held := false
	if reader, ok := r.taskUpdater.(taskColumnReader); ok {
		if after, rerr := reader.GetTask(ctx, job.RepositoryID, job.Task.ID); rerr == nil {
			held = after.Column == job.Task.Column
		}
	}
	if held {
		// Accepted but held: require_human_review turns an approval into a
		// recorded verdict and keeps the card here for the person. That is the
		// gate working, not a failure — and not something to leave a
		// "move it by hand" comment about.
		log.Info().Str("task_id", job.Task.ID.String()).
			Msg("review verdict finalize: approval recorded, task held for human review")
		return true, nil
	}
	log.Info().Str("task_id", job.Task.ID.String()).Str("column", string(target)).
		Msg("review verdict finalize: verdict recorded, task moved")
	return true, nil
}

// verdictColumn reads the one-word answer. Deliberately strict about which word
// it accepts and deliberately loose about what surrounds it: models wrap a
// single word in backticks or a full stop, but a reply that argues both sides is
// not a verdict and must not be read as one.
func verdictColumn(answer string, exit domain.TaskColumn) (domain.TaskColumn, bool) {
	word := strings.ToUpper(strings.Trim(strings.TrimSpace(answer), "`*_.!\"' \n\t"))
	switch {
	case word == "APPROVE":
		return exit, true
	case word == "REVISE":
		return domain.TaskColumnNeedRevision, true
	default:
		return "", false
	}
}

// sweepReviewVerdict is the last thing a review run is asked: where does the
// task go.
//
// An implementation run has advanceToCodeReview behind it — the runner moves
// the card once the branch proves work happened. A review run has nothing: its
// only exit is the reviewer remembering to call move_board_task, and one that
// writes a complete "the changes deliver the functionality, the pipeline is
// green" verdict and then stops leaves the task parked in code_review with a
// completed run above it. Nothing retries it, because the run succeeded.
//
// One bounded turn on the same history, with the verdict still in context,
// turns that dead end into the move it already decided on. It never moves the
// task itself: a verdict the reviewer will not state is not a verdict, and
// promoting work to ready_for_qa from the runner is exactly the unreviewed
// green the column exists to prevent.
//
// The return value is a usage-limit block hit anywhere in the sweep — its own
// turn or the finalize turn it falls back to — so the caller parks the task
// on it instead of leaving the column to a run that never actually answered.
func (r *Runner) sweepReviewVerdict(
	ctx context.Context,
	job RunJob,
	agentRec domain.Agent,
	history []domain.Message,
	resp domain.AgentResponse,
	model string,
	policy domain.ToolPolicy,
) *domain.QuotaBlock {
	if resp.Clarification != nil || resp.ResourceBlock != nil {
		return nil
	}
	if job.Task.TaskType == domain.TaskTypeAnaliz {
		return nil
	}
	exit, ok := reviewExitColumn(job.Task.Column, job.BranchFlow)
	if !ok {
		return nil
	}
	// The agent may well have moved the task during its run; sweeping then would
	// ask a finished reviewer to re-decide a decision already on the board.
	reader, ok := r.taskUpdater.(taskColumnReader)
	if !ok {
		return nil
	}
	fresh, err := reader.GetTask(ctx, job.RepositoryID, job.Task.ID)
	if err != nil {
		log.Warn().Err(err).Str("task_id", job.Task.ID.String()).Msg("review sweep: task re-read failed, leaving the column to the agent")
		return nil
	}
	if fresh.Column != job.Task.Column {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("Your review is finished but the task is still in `" + string(job.Task.Column) + "` — you did not record where it goes, " +
		"so the board shows it as still under review and nobody picks it up.\n")

	// The verdicts come first because they are what the move is refused over:
	// criteriaReviewGate rejects the forward exit while any criterion is missing
	// this role's ruling, and a QA round that met that wall answered "it seems I
	// cannot move the task directly", left the criteria unruled, and parked the
	// card in in_qa across three more waves that each re-planned instead of
	// recording anything.
	if role, ok := criterionReviewRole(job.Task.Column); ok {
		if missing := r.missingVerdicts(ctx, job, role); len(missing) > 0 {
			sb.WriteString("\nFirst, the acceptance criteria you have not ruled on. The forward move is REFUSED while any of these " +
				"lacks your " + string(role) + " verdict — that refusal is what you hit if you already tried to move the task:\n")
			for _, c := range missing {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", c.ID, c.Text))
			}
			sb.WriteString("For EACH id above call review_criterion now, from what you executed in this run: approve it when your " +
				"own run covered it, reject it with an expected-vs-actual note when it failed or you could not exercise it. " +
				"Do not approve anything you did not observe.\n")
		}
	}

	sb.WriteString("\nThen leave the column, based on the verdict you just gave:\n" +
		"1. Everything you required is satisfied → call move_board_task to `" + string(exit) + "`.\n" +
		"2. Anything you flagged still needs work → call move_board_task to `need_revision`, and make sure your findings are on the task as a numbered comment.\n" +
		"If the move is refused, read the error: it names exactly what is missing, and fixing that and retrying the move is part of this run. " +
		"Do not re-review, do not start new testing, and do not change your verdict.")
	prompt := sb.String()

	rec := activity.FromContext(ctx)
	if rec != nil {
		rec.Step("review_verdict_sweep_start", map[string]any{"column": string(job.Task.Column)})
	}
	history = append(history,
		domain.Message{Role: domain.RoleAssistant, Content: resp.Message.Content},
		domain.Message{Role: domain.RoleUser, Content: prompt},
	)
	if _, err := r.agentLoop.RunTask(ctx, history, model, agentRec.ProviderType, policy,
		agent.WithLightModel(agentRec.Model),
		agent.WithCLILabel(job.Task.Key+" review-sweep", job.Task.Title)); err != nil {
		if quotaErr, ok := domain.QuotaBlockOf(err); ok {
			return quotaErr
		}
		log.Warn().Err(err).Str("task_id", job.Task.ID.String()).Msg("review verdict sweep failed; task stays in its review column")
		return nil
	}
	if after, err := reader.GetTask(ctx, job.RepositoryID, job.Task.ID); err == nil && after.Column == job.Task.Column {
		log.Info().Str("task_id", job.Task.ID.String()).Str("column", string(job.Task.Column)).
			Msg("review verdict sweep ran and the task is still in its review column")
		// Last resort before the card is left to a human: ask for the verdict
		// alone — one word, no tool call — and make the move from here. See
		// finalizeReviewVerdict for why that is not the unreviewed promotion this
		// column exists to prevent.
		moved, quotaErr := r.finalizeReviewVerdict(ctx, job, agentRec, history, model, policy, exit)
		if quotaErr != nil {
			return quotaErr
		}
		if moved {
			return nil
		}
		if r.taskUpdater != nil {
			if _, cErr := r.taskUpdater.AddComment(ctx, job.RepositoryID, job.Task.ID, domain.CreateTaskCommentRequest{
				AuthorType: "system",
				Content: "Review tamamlandı ama kart hâlâ `" + string(job.Task.Column) + "` kolonunda: değerlendirme sonrası " +
					"`" + string(exit) + "` veya `need_revision` geçişi yapılmadı." + r.stuckVerdictNote(ctx, job) +
					" Kolonu elle taşımak gerekiyor.",
			}); cErr != nil {
				log.Warn().Err(cErr).Str("task_id", job.Task.ID.String()).Msg("review sweep: stuck-column comment failed")
			}
		}
	}
	return nil
}
