// Package opencode delegates one board task to a headless OpenCode CLI
// session running on this host, the same pattern claudecode and antigravity
// use: the board runner still clones the repository, checks out the task
// branch, and does everything after the session (verify gate, commit, PR,
// column advance). OpenCode is handed a prepared workspace and gives back a
// closing message.
//
// OpenCode's own tool surface reaches TaskTrooper's board tools through
// OPENCODE_CONFIG_CONTENT (see mcp.go) — an env var carrying inline JSON the
// CLI merges over the project's own opencode.json — rather than a file or a
// per-invocation flag. There is no documented --max-turns equivalent and no
// separate system-prompt flag, so a run's whole history is folded into the
// one positional prompt `opencode run` takes.
package opencode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	usageapp "github.com/makifbaysal/tasktrooper/server/internal/application/usage"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
	"github.com/makifbaysal/tasktrooper/server/internal/port"
)

// DefaultRunTimeout bounds ONE session end to end.
const DefaultRunTimeout = time.Hour

const stderrTailMax = 8 << 10

// Config drives one Executor.
type Config struct {
	// Binary is the CLI to run; empty means "opencode", resolved on PATH.
	Binary string
	// RunTimeout bounds one session; <= 0 means DefaultRunTimeout.
	RunTimeout  time.Duration
	MCP         MCPConfig
	MCPProvider MCPProvider
	// Limiter is the machine-wide session bound shared with the other CLI
	// runtimes. Nil runs unbounded, as this executor always did.
	Limiter port.SessionLimiter
}

// Executor runs board tasks through the OpenCode CLI. It satisfies
// port.TaskExecutor.
type Executor struct {
	limiter     port.SessionLimiter
	bin         string
	runTimeout  time.Duration
	mcp         MCPConfig
	mcpProvider MCPProvider
	now         func() time.Time

	// gateMu guards the account-wide rate-limit gate — see cursor's and
	// claudecode's identical gate for the reasoning.
	gateMu      sync.Mutex
	quotaUntil  time.Time
	quotaDetail string
}

var _ port.TaskExecutor = (*Executor)(nil)

// New resolves the binary and returns the executor, or an error when the
// binary is not on PATH.
func New(cfg Config) (*Executor, error) {
	resolved, err := ResolveBinary(cfg.Binary)
	if err != nil {
		return nil, fmt.Errorf("opencode executor: %w", err)
	}
	runTimeout := cfg.RunTimeout
	if runTimeout <= 0 {
		runTimeout = DefaultRunTimeout
	}
	return &Executor{
		limiter:     cfg.Limiter,
		bin:         resolved,
		runTimeout:  runTimeout,
		mcp:         cfg.MCP,
		mcpProvider: cfg.MCPProvider,
		now:         time.Now,
	}, nil
}

// Supports answers for the one provider this executor exists for.
func (e *Executor) Supports(provider domain.LLMProviderType) bool {
	return e != nil && provider == domain.LLMProviderOpencode
}

// armQuotaGate records that a session on this executor hit a rate limit —
// see claudecode's identical method for the reasoning.
func (e *Executor) armQuotaGate(block *domain.QuotaBlock) {
	if block == nil {
		return
	}
	e.gateMu.Lock()
	defer e.gateMu.Unlock()
	if block.ResumeAt.After(e.quotaUntil) {
		e.quotaUntil = block.ResumeAt
		e.quotaDetail = block.Detail
	}
}

// clearQuotaGate lifts the gate once a session proves the limit is no longer
// in force.
func (e *Executor) clearQuotaGate() {
	e.gateMu.Lock()
	defer e.gateMu.Unlock()
	e.quotaUntil = time.Time{}
	e.quotaDetail = ""
}

// QuotaGate reports the gate's current state, for observability.
func (e *Executor) QuotaGate() (until time.Time, armed bool) {
	e.gateMu.Lock()
	defer e.gateMu.Unlock()
	return e.quotaUntil, !e.quotaUntil.IsZero()
}

func (e *Executor) quotaGateState() (until time.Time, detail string, armed bool) {
	e.gateMu.Lock()
	defer e.gateMu.Unlock()
	return e.quotaUntil, e.quotaDetail, !e.quotaUntil.IsZero()
}

// gatedQuotaBlock is the park Execute returns while the gate is armed, or nil.
func (e *Executor) gatedQuotaBlock(req domain.TaskExecution) *domain.QuotaBlock {
	until, detail, armed := e.quotaGateState()
	if !armed || !e.now().Before(until) {
		return nil
	}
	log.Info().
		Str("task_key", req.TaskKey).
		Time("resume_at", until).
		Msg("opencode rate limit gate is armed; parking without spawning")
	return &domain.QuotaBlock{
		ResumeAt:     until,
		CLISessionID: req.ResumeSessionID,
		Detail:       "another OpenCode session hit a provider rate limit: " + detail,
		Provider:     domain.LLMProviderOpencode,
	}
}

// Execute runs the task in an OpenCode session and maps its outcome onto the
// response shape the board runner already reads.
func (e *Executor) Execute(ctx context.Context, req domain.TaskExecution) (domain.AgentResponse, error) {
	if e == nil {
		return domain.AgentResponse{}, errors.New("opencode executor is not configured")
	}
	if strings.TrimSpace(req.WorkDir) == "" {
		return domain.AgentResponse{}, errors.New("opencode executor: no task workspace to run in")
	}
	if block := e.gatedQuotaBlock(req); block != nil {
		return domain.AgentResponse{}, block
	}
	if e.limiter != nil {
		release, err := e.limiter.Acquire(ctx)
		if err != nil {
			return domain.AgentResponse{}, err
		}
		defer release()
	}

	mcpCfg, releaseMCP, err := e.resolveMCP(ctx, MCPRun{Policy: req.Policy, Label: req.TaskKey})
	defer releaseMCP()
	if err != nil {
		return domain.AgentResponse{}, err
	}
	configContent, err := mcpConfigContentEnv(mcpCfg)
	if err != nil {
		return domain.AgentResponse{}, err
	}

	s, err := e.spawn(ctx, invocation{
		workDir:       req.WorkDir,
		prompt:        flattenHistory(req.History),
		model:         req.Model,
		label:         req.TaskKey,
		configContent: configContent,
	})
	if err != nil {
		return domain.AgentResponse{}, err
	}
	return e.finish(ctx, req.TaskKey, s)
}

// invocation is one spawn of the CLI.
type invocation struct {
	workDir       string
	prompt        string
	model         string
	label         string
	configContent string
	stream        port.ChatStream
}

// buildArgs renders the opencode command line for one spawn.
func (e *Executor) buildArgs(inv invocation) []string {
	args := []string{
		"run", inv.prompt,
		"--format", "json",
		// Without this a run only PROPOSES edits and applies nothing; there
		// is no human at this terminal to approve them.
		"--auto",
	}
	// An empty model hands the choice to opencode's own configured default,
	// the same "omit rather than guess" rule claudecode's --model follows.
	// OpenCode's own flag takes "provider/model", not a bare model name — the
	// caller is expected to already carry it in that shape.
	if model := strings.TrimSpace(inv.model); model != "" {
		args = append(args, "--model", model)
	}
	return args
}

func (e *Executor) spawn(ctx context.Context, inv invocation) (session, error) {
	runCtx, cancel := context.WithTimeout(ctx, e.runTimeout)
	defer cancel()

	args := e.buildArgs(inv)
	cmd := exec.CommandContext(runCtx, e.bin, args...)
	cmd.Dir = inv.workDir
	cmd.Env = childEnv(ctx)
	if inv.configContent != "" {
		cmd.Env = append(cmd.Env, "OPENCODE_CONFIG_CONTENT="+inv.configContent)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return session{}, fmt.Errorf("opencode stdout: %w", err)
	}
	stderr := &tailWriter{max: stderrTailMax}
	// A known opencode issue (run --format json can hang forever after a 429
	// instead of exiting: the provider error reaches stderr but the process
	// never emits its own error event or returns) means waiting for cmd.Wait
	// alone would occupy this run's slot for the full runTimeout on every
	// rate limit. Watching stderr AS IT ARRIVES for the same text
	// quotaBlockFrom matches and cancelling runCtx the moment it appears lets
	// this run recognise and park on the limit immediately, the same as a
	// session that exits cleanly.
	watcher := &rateLimitWatcher{cancel: cancel}
	cmd.Stderr = io.MultiWriter(stderr, watcher)

	if err := cmd.Start(); err != nil {
		return session{}, fmt.Errorf("start opencode: %w", err)
	}

	out, parseErr := parseStream(stdout, newStreamingSink(&traceSink{ctx: ctx, taskKey: inv.label}, inv.stream))
	if parseErr != nil {
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil

	return session{
		out:        out,
		stderrTail: stderr.String(),
		parseErr:   parseErr,
		waitErr:    waitErr,
		timedOut:   timedOut,
	}, nil
}

type session struct {
	out        outcome
	stderrTail string
	parseErr   error
	waitErr    error
	timedOut   bool
}

func (e *Executor) finish(ctx context.Context, label string, s session) (domain.AgentResponse, error) {
	resp, err := e.finishInner(ctx, label, s)
	if err == nil {
		e.clearQuotaGate()
		return resp, nil
	}
	if block, ok := domain.QuotaBlockOf(err); ok {
		e.armQuotaGate(block)
	}
	return resp, err
}

func (e *Executor) finishInner(ctx context.Context, label string, s session) (domain.AgentResponse, error) {
	out := s.out
	usageapp.TokenUsageFromContext(ctx).Add(out.Usage)

	if s.timedOut {
		return domain.AgentResponse{}, fmt.Errorf("opencode did not finish within %s and was stopped: %s",
			e.runTimeout, domain.TruncateHead(strings.TrimSpace(s.stderrTail), 500))
	}
	if s.parseErr != nil {
		return domain.AgentResponse{}, s.parseErr
	}
	// Quota detection runs on a session that either never produced a result or
	// reported one as an error — a healthy session's own text is never checked
	// against the pattern, the same asymmetry claudecode's finish keeps.
	if !out.SawResult || out.IsError {
		if block := quotaBlockFrom(out, s.stderrTail, out.SessionID, e.now()); block != nil {
			log.Warn().
				Str("task_key", label).
				Str("cli_session_id", out.SessionID).
				Time("resume_at", block.ResumeAt).
				Msg("opencode provider rate limit reached, parking the task")
			return domain.AgentResponse{}, block
		}
	}
	// A known opencode issue (run --format json can exit cleanly without ever
	// emitting the final step_finish event) means a missing terminal event
	// with a clean exit and real output is treated as success, unlike
	// claudecode/antigravity where no terminal event is always a failure.
	if !out.SawResult {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return domain.AgentResponse{}, ctxErr
		}
		if s.waitErr != nil || strings.TrimSpace(out.Text) == "" {
			if sig, ok := domain.ExitSignal(s.waitErr); ok {
				return domain.AgentResponse{}, fmt.Errorf(
					"opencode was killed by signal %s from outside this run: %s",
					sig, domain.TruncateHead(strings.TrimSpace(s.stderrTail), 500))
			}
			return domain.AgentResponse{}, fmt.Errorf("opencode ended without a result (%v): %s",
				s.waitErr, domain.TruncateHead(strings.TrimSpace(s.stderrTail), 500))
		}
	}
	if out.IsError {
		return domain.AgentResponse{}, fmt.Errorf("opencode failed (%s): %s",
			out.Status, domain.TruncateHead(firstNonEmpty(out.Text, strings.TrimSpace(s.stderrTail)), 1000))
	}
	if s.waitErr != nil {
		return domain.AgentResponse{}, fmt.Errorf("opencode exited with an error after reporting success (%v): %s",
			s.waitErr, domain.TruncateHead(strings.TrimSpace(s.stderrTail), 500))
	}
	if strings.TrimSpace(out.Text) == "" {
		return domain.AgentResponse{}, errors.New("opencode finished without producing any answer")
	}
	return domain.AgentResponse{
		Message: domain.Message{Role: domain.RoleAssistant, Content: out.Text},
		Usage:   out.Usage,
	}, nil
}

// flattenHistory folds the runner's message list into the single positional
// prompt `opencode run` takes.
func flattenHistory(history []domain.Message) string {
	var parts []string
	for _, msg := range history {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if msg.Role == domain.RoleAssistant {
			content = "Earlier assistant turn:\n" + content
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n\n")
}

type tailWriter struct {
	max int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.max > 0 && n > w.max {
		p = p[n-w.max:]
	}
	w.buf = append(w.buf, p...)
	if w.max > 0 && len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	return n, nil
}

func (w *tailWriter) String() string { return string(bytes.TrimSpace(w.buf)) }

// rateLimitWatcher scans stderr as it streams in for the same rate-limit text
// quotaBlockFrom matches after the fact, and cancels the run the moment it
// appears — see the comment where it is installed in spawn for why a
// clean-exit-only check is not enough for this CLI.
//
// cmd.Stderr is written from ONE internal goroutine per exec.Cmd (started by
// cmd.Start when Stderr is not an *os.File), so this needs no lock of its
// own: Write is never called concurrently with itself.
type rateLimitWatcher struct {
	cancel context.CancelFunc
	buf    []byte
	fired  bool
}

// watcherScanWindow bounds how much of stderr is kept for matching — enough
// for the pattern to appear inside one write, not so much that a chatty CLI
// makes every write re-scan an ever-growing buffer.
const watcherScanWindow = 4 << 10

func (w *rateLimitWatcher) Write(p []byte) (int, error) {
	if w.fired {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) > watcherScanWindow {
		w.buf = w.buf[len(w.buf)-watcherScanWindow:]
	}
	if rateLimitPattern.Match(w.buf) {
		w.fired = true
		w.cancel()
	}
	return len(p), nil
}
