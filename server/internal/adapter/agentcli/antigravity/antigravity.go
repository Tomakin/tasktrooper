// Package antigravity delegates one board task to a headless Antigravity
// (AGY) CLI session running on this host, the same pattern claudecode uses for
// Claude Code: the board runner still clones the repository, checks out the
// task branch, and does everything after the session (verify gate, commit,
// PR, column advance). AGY is handed a prepared workspace and gives back a
// closing message.
//
// AGY's own tool surface reaches TaskTrooper's board tools through a
// workspace-level MCP config file (see mcp.go) rather than through a
// per-invocation flag the way claudecode's --mcp-config works — AGY has no
// such flag, only a path it reads on its own from the directory it is started
// in. There is also no --max-turns equivalent (see probe.go) and no confirmed
// separate system-prompt flag, so a run's whole history is folded into the one
// positional prompt AGY's -p takes.
package antigravity

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

// DefaultRunTimeout bounds ONE session end to end, the way claudecode's does —
// nothing else in this path ever gives up on a wedged subprocess.
const DefaultRunTimeout = time.Hour

// stderrTailMax is how much of the child's stderr is kept for a failure
// message — the tail, since a dying CLI explains itself on its last lines.
const stderrTailMax = 8 << 10

// Config drives one Executor.
type Config struct {
	// Binary is the CLI to run; empty means "agy", resolved on PATH.
	Binary string
	// RunTimeout bounds one session; <= 0 means DefaultRunTimeout.
	RunTimeout time.Duration
	// MCP is a fixed endpoint for every run; used by tests and by any caller
	// with one endpoint and one credential. See mcp.go.
	MCP MCPConfig
	// MCPProvider mints a per-run endpoint and credential instead, and wins
	// over MCP when set.
	MCPProvider MCPProvider
	// Limiter is the machine-wide session bound shared with the other CLI
	// runtimes. Nil runs unbounded, as this executor always did.
	Limiter port.SessionLimiter
}

// Executor runs board tasks through the Antigravity CLI. It satisfies
// port.TaskExecutor.
type Executor struct {
	limiter     port.SessionLimiter
	bin         string
	runTimeout  time.Duration
	mcp         MCPConfig
	mcpProvider MCPProvider
	now         func() time.Time

	// gateMu guards the account-wide quota gate — see cursor's and
	// claudecode's identical gate for the reasoning.
	gateMu      sync.Mutex
	quotaUntil  time.Time
	quotaDetail string
}

var _ port.TaskExecutor = (*Executor)(nil)

// New resolves the binary and returns the executor, or an error when the
// binary is not on PATH. platform/runtime skips registration entirely on an
// error, so a host without the CLI has no antigravity executor at all.
func New(cfg Config) (*Executor, error) {
	resolved, err := ResolveBinary(cfg.Binary)
	if err != nil {
		return nil, fmt.Errorf("antigravity executor: %w", err)
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

// Supports answers for the one provider this executor exists for. Nil-safe so
// a runner holding a nil executor asks the same question and gets "no".
func (e *Executor) Supports(provider domain.LLMProviderType) bool {
	return e != nil && provider == domain.LLMProviderAntigravity
}

// armQuotaGate records that a session on this executor hit the quota — see
// claudecode's identical method for the reasoning.
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
		Msg("antigravity quota gate is armed; parking without spawning")
	return &domain.QuotaBlock{
		ResumeAt:     until,
		CLISessionID: req.ResumeSessionID,
		Detail:       "another Antigravity session hit the quota: " + detail,
		Provider:     domain.LLMProviderAntigravity,
	}
}

// Execute runs the task in an AGY session and maps its outcome onto the
// response shape the board runner already reads.
func (e *Executor) Execute(ctx context.Context, req domain.TaskExecution) (domain.AgentResponse, error) {
	if e == nil {
		return domain.AgentResponse{}, errors.New("antigravity executor is not configured")
	}
	// Never fall back to a default directory — see claudecode.Execute for why.
	if strings.TrimSpace(req.WorkDir) == "" {
		return domain.AgentResponse{}, errors.New("antigravity executor: no task workspace to run in")
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

	cleanupMCP, err := writeMCPConfigFile(req.WorkDir, mcpCfg)
	if err != nil {
		return domain.AgentResponse{}, err
	}
	defer cleanupMCP()

	s, err := e.spawn(ctx, invocation{
		workDir: req.WorkDir,
		prompt:  flattenHistory(req.History),
		model:   req.Model,
		label:   req.TaskKey,
	})
	if err != nil {
		return domain.AgentResponse{}, err
	}
	return e.finish(ctx, req.TaskKey, s)
}

// invocation is one spawn of the CLI.
type invocation struct {
	workDir string
	prompt  string
	model   string
	label   string
	// stream forwards assistant text as it arrives. The zero value reports
	// nothing, which is what a board run wants.
	stream port.ChatStream
}

// buildArgs renders the AGY command line for one spawn.
//
// Order is fixed so two runs with the same inputs produce byte-identical
// command lines, which is what makes a failure reproducible from a log line.
func (e *Executor) buildArgs(inv invocation) []string {
	args := []string{
		"-p", inv.prompt,
		"--output-format", "stream-json",
		// The workspace is a throwaway clone on a task branch and there is no
		// human at this terminal to answer a prompt.
		"--dangerously-skip-permissions",
	}
	// An empty model hands the choice to AGY's own configured default, the
	// same "omit rather than guess" rule claudecode's --model follows.
	if model := strings.TrimSpace(inv.model); model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// spawn runs one CLI session to completion and returns everything it
// produced. The error return is only for failures BEFORE the child was
// running; everything the session itself did comes back in the session value.
func (e *Executor) spawn(ctx context.Context, inv invocation) (session, error) {
	runCtx, cancel := context.WithTimeout(ctx, e.runTimeout)
	defer cancel()

	args := e.buildArgs(inv)
	cmd := exec.CommandContext(runCtx, e.bin, args...)
	cmd.Dir = inv.workDir
	cmd.Env = childEnv(ctx)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return session{}, fmt.Errorf("antigravity stdout: %w", err)
	}
	stderr := &tailWriter{max: stderrTailMax}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return session{}, fmt.Errorf("start agy: %w", err)
	}

	// streamingSink is a pass-through when nobody is listening, so a board run
	// pays nothing for the chat path existing.
	out, parseErr := parseStream(stdout, newStreamingSink(&traceSink{ctx: ctx, taskKey: inv.label}, inv.stream))
	// A parse that stopped early left the pipe with unread bytes in it; Wait
	// would otherwise block on a full pipe nobody is draining.
	if parseErr != nil {
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()

	// A deadline this executor imposed, not one the caller did.
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil

	return session{
		out:        out,
		stderrTail: stderr.String(),
		parseErr:   parseErr,
		waitErr:    waitErr,
		timedOut:   timedOut,
	}, nil
}

// session is everything one CLI invocation produced.
type session struct {
	out        outcome
	stderrTail string
	parseErr   error
	waitErr    error
	timedOut   bool
}

// finish turns the session's outcome into either a response or an error.
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
		return domain.AgentResponse{}, fmt.Errorf(
			"antigravity did not finish within %s and was stopped: %s",
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
				Msg("antigravity quota reached, parking the task")
			return domain.AgentResponse{}, block
		}
	}
	// No terminal event means the session did not finish: killed, crashed, or
	// stopped by the run's own cancellation.
	if !out.SawResult {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return domain.AgentResponse{}, ctxErr
		}
		if sig, ok := domain.ExitSignal(s.waitErr); ok {
			return domain.AgentResponse{}, fmt.Errorf(
				"antigravity was killed by signal %s from outside this run: %s",
				sig, domain.TruncateHead(strings.TrimSpace(s.stderrTail), 500))
		}
		return domain.AgentResponse{}, fmt.Errorf("antigravity ended without a result (%v): %s",
			s.waitErr, domain.TruncateHead(strings.TrimSpace(s.stderrTail), 500))
	}
	if out.IsError {
		return domain.AgentResponse{}, fmt.Errorf("antigravity failed (%s): %s",
			out.Status, domain.TruncateHead(firstNonEmpty(out.Text, strings.TrimSpace(s.stderrTail)), 1000))
	}
	if s.waitErr != nil {
		// A success event with a non-zero exit is contradictory; trust the exit
		// code, which is the kernel's report rather than the process's own.
		return domain.AgentResponse{}, fmt.Errorf("antigravity exited with an error after reporting success (%v): %s",
			s.waitErr, domain.TruncateHead(strings.TrimSpace(s.stderrTail), 500))
	}
	if strings.TrimSpace(out.Text) == "" {
		return domain.AgentResponse{}, errors.New("antigravity finished without producing any answer")
	}
	return domain.AgentResponse{
		Message: domain.Message{Role: domain.RoleAssistant, Content: out.Text},
		Usage:   out.Usage,
	}, nil
}

// flattenHistory folds the runner's message list into the single positional
// prompt AGY's -p takes. Unlike claudecode there is no separate
// --append-system-prompt flag, so system blocks are joined in ahead of the
// rest rather than split out.
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

// childEnv is defined in probe.go and reused here — one answer to "what
// environment does a spawned agy get" for both the probe and a real run.

// tailWriter keeps the LAST max bytes written to it, so a failing CLI's
// explanation on its final lines survives an otherwise-unbounded stderr.
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
