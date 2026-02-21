package shared

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	containershared "github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// Result represents the outcome of running the loop.
type Result struct {
	// LoopsCompleted is the number of loops that ran.
	LoopsCompleted int

	// FinalStatus is the last parsed status.
	FinalStatus *Status

	// ExitReason describes why the loop exited.
	ExitReason string

	// Session is the final session state.
	Session *Session

	// Error is set if the loop exited due to an error.
	Error error

	// RateLimitHit is true if the loop hit Claude's API rate limit.
	RateLimitHit bool
}

// ContainerStartConfig is returned by the per-iteration container factory.
// It provides the container ID and a cleanup function that removes the
// container and its associated volumes.
type ContainerStartConfig struct {
	ContainerID string
	Cleanup     func()
}

// Options configures the loop execution.
type Options struct {
	// CreateContainer creates a new container for each iteration.
	// Returns a ContainerStartConfig with the container ID and cleanup function.
	CreateContainer func(ctx context.Context) (*ContainerStartConfig, error)

	// ProjectCfg is the project configuration.
	ProjectCfg *config.Project

	// Agent is the agent name.
	Agent string

	// Prompt is the prompt used for each iteration (-p flag).
	Prompt string

	// MaxLoops is the maximum number of loops to run.
	MaxLoops int

	// StagnationThreshold is how many loops without progress before tripping.
	StagnationThreshold int

	// Timeout is the per-loop timeout.
	Timeout time.Duration

	// ResetCircuit resets the circuit breaker before starting.
	ResetCircuit bool

	// CallsPerHour is the rate limit (0 to disable).
	CallsPerHour int

	// CompletionThreshold is the number of completion indicators required.
	CompletionThreshold int

	// SessionExpirationHours is the session TTL (0 for default).
	SessionExpirationHours int

	// SameErrorThreshold is how many same-error loops before tripping.
	SameErrorThreshold int

	// OutputDeclineThreshold is the percentage decline that triggers trip.
	OutputDeclineThreshold int

	// MaxConsecutiveTestLoops is how many test-only loops before tripping.
	MaxConsecutiveTestLoops int

	// LoopDelaySeconds is the delay between loop iterations.
	LoopDelaySeconds int

	// SafetyCompletionThreshold is loops with completion indicators but no exit signal before trip.
	SafetyCompletionThreshold int

	// UseStrictCompletion requires both EXIT_SIGNAL and completion indicators.
	UseStrictCompletion bool

	// SkipPermissions passes --dangerously-skip-permissions to claude.
	SkipPermissions bool

	// SystemPrompt is the full system prompt appended via --append-system-prompt.
	// Built by BuildSystemPrompt() which combines the default LOOP_STATUS
	// instructions with any user-provided additional instructions.
	SystemPrompt string

	// Monitor is the optional monitor for live output.
	Monitor *Monitor

	// WorkDir is the host working directory for this session.
	WorkDir string

	// Logger is the diagnostic file logger (from IOStreams.Logger).
	Logger iostreams.Logger

	// Verbose enables verbose logging.
	Verbose bool

	// OnLoopStart is called before each loop iteration.
	OnLoopStart func(loopNum int)

	// OnLoopEnd is called after each loop iteration with optional cost data.
	OnLoopEnd func(loopNum int, status *Status, resultEvent *ResultEvent, err error)

	// OnOutput is called with text output chunks during execution.
	OnOutput func(chunk []byte)

	// OnStreamEvent is called for each raw streaming event (content_block_start,
	// content_block_delta, etc.). Enables rich TUI updates like tool activity
	// indicators. When set alongside OnOutput, both fire independently.
	OnStreamEvent func(*StreamDeltaEvent)

	// OnRateLimitHit is called when Claude's API limit is detected.
	// Return true to wait and retry, false to exit.
	OnRateLimitHit func() bool
}

// Runner executes autonomous loops.
type Runner struct {
	client  *docker.Client
	store   *SessionStore
	history *HistoryStore
}

// NewRunner creates a new Runner with the given Docker client and default stores.
func NewRunner(client *docker.Client) (*Runner, error) {
	store, err := DefaultSessionStore()
	if err != nil {
		return nil, err
	}
	history, err := DefaultHistoryStore()
	if err != nil {
		return nil, err
	}
	return &Runner{
		client:  client,
		store:   store,
		history: history,
	}, nil
}

// NewRunnerWith creates a Runner with explicit store and history dependencies.
// This is useful for testing with custom storage directories.
func NewRunnerWith(client *docker.Client, store *SessionStore, history *HistoryStore) *Runner {
	return &Runner{
		client:  client,
		store:   store,
		history: history,
	}
}

// Run executes the loop until completion, error, or max loops.
// Each iteration creates a new container via opts.CreateContainer, attaches,
// parses stream-json output, and cleans up the container.
func (r *Runner) Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Logger == nil {
		return nil, fmt.Errorf("opts.Logger is required")
	}

	// Set defaults
	if opts.MaxLoops <= 0 {
		opts.MaxLoops = DefaultMaxLoops
	}
	if opts.StagnationThreshold <= 0 {
		opts.StagnationThreshold = DefaultStagnationThreshold
	}
	if opts.Timeout <= 0 {
		opts.Timeout = time.Duration(DefaultTimeoutMinutes) * time.Minute
	}
	if opts.CompletionThreshold <= 0 {
		opts.CompletionThreshold = DefaultCompletionThreshold
	}
	if opts.SessionExpirationHours <= 0 {
		opts.SessionExpirationHours = DefaultSessionExpirationHours
	}
	if opts.SameErrorThreshold <= 0 {
		opts.SameErrorThreshold = DefaultSameErrorThreshold
	}
	if opts.OutputDeclineThreshold <= 0 {
		opts.OutputDeclineThreshold = DefaultOutputDeclineThreshold
	}
	if opts.MaxConsecutiveTestLoops <= 0 {
		opts.MaxConsecutiveTestLoops = DefaultMaxConsecutiveTestLoops
	}
	if opts.CallsPerHour == 0 {
		opts.CallsPerHour = DefaultCallsPerHour
	}

	// Load or create session (with expiration check)
	session, expired, err := r.store.LoadSessionWithExpiration(opts.ProjectCfg.Name, opts.Agent, opts.SessionExpirationHours)
	if err != nil {
		opts.Logger.Error().Err(err).Msg("failed to load session")
		return &Result{
			ExitReason: "failed to load session",
			Error:      fmt.Errorf("failed to load session (use --reset-circuit --all to start fresh): %w", err),
		}, nil
	}
	if expired {
		opts.Logger.Debug().Msg("session expired, starting fresh")
		if histErr := r.history.AddSessionEntry(opts.ProjectCfg.Name, opts.Agent, "expired", "", "", 0); histErr != nil {
			opts.Logger.Warn().Err(histErr).Msg("failed to record session expiration in history")
		}
	}
	sessionCreated := session == nil
	if session == nil {
		session = NewSession(opts.ProjectCfg.Name, opts.Agent, opts.Prompt, opts.WorkDir)
	}
	if sessionCreated {
		if histErr := r.history.AddSessionEntry(opts.ProjectCfg.Name, opts.Agent, "created", StatusPending, "", 0); histErr != nil {
			opts.Logger.Warn().Err(histErr).Msg("failed to record session creation in history")
		}
		if saveErr := r.store.SaveSession(session); saveErr != nil {
			opts.Logger.Error().Err(saveErr).Msg("failed to save initial session")
			return &Result{
				Session:    session,
				ExitReason: "failed to save initial session",
				Error:      fmt.Errorf("failed to save initial session: %w", saveErr),
			}, nil
		}
	}

	// Initialize rate limiter
	rateLimiter := NewRateLimiter(opts.CallsPerHour)
	if session.RateLimitState != nil {
		if !rateLimiter.RestoreState(*session.RateLimitState) {
			opts.Logger.Warn().Msg("rate limit state expired or invalid, starting fresh window")
		}
	}

	// Initialize circuit breaker
	circuit := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		StagnationThreshold:       opts.StagnationThreshold,
		SameErrorThreshold:        opts.SameErrorThreshold,
		OutputDeclineThreshold:    opts.OutputDeclineThreshold,
		MaxConsecutiveTestLoops:   opts.MaxConsecutiveTestLoops,
		CompletionThreshold:       opts.CompletionThreshold,
		SafetyCompletionThreshold: opts.SafetyCompletionThreshold,
	})

	// Reset circuit if requested
	if opts.ResetCircuit {
		if err := r.store.DeleteCircuitState(opts.ProjectCfg.Name, opts.Agent); err != nil {
			opts.Logger.Warn().Err(err).Msg("failed to delete circuit state")
			return &Result{
				Session:    session,
				ExitReason: "failed to reset circuit breaker",
				Error:      fmt.Errorf("failed to reset circuit breaker as requested: %w", err),
			}, nil
		}
		if histErr := r.history.AddCircuitEntry(opts.ProjectCfg.Name, opts.Agent, "tripped", "closed", "manual reset", 0, 0, 0, 0); histErr != nil {
			opts.Logger.Warn().Err(histErr).Msg("failed to record circuit reset in history")
		}
	} else {
		circuitState, err := r.store.LoadCircuitState(opts.ProjectCfg.Name, opts.Agent)
		if err != nil {
			opts.Logger.Error().Err(err).Msg("failed to load circuit state - refusing to run")
			return &Result{
				Session:    session,
				ExitReason: "failed to load circuit state",
				Error:      fmt.Errorf("failed to load circuit state (may be tripped): %w", err),
			}, nil
		}
		if circuitState != nil && circuitState.Tripped {
			return &Result{
				Session:    session,
				ExitReason: fmt.Sprintf("circuit already tripped: %s", circuitState.TripReason),
				Error:      fmt.Errorf("circuit breaker tripped: %s", circuitState.TripReason),
			}, nil
		}
	}

	result := &Result{
		Session: session,
	}

	// Update monitor with rate limiter if available
	if opts.Monitor != nil {
		opts.Monitor.opts.RateLimiter = rateLimiter
		opts.Monitor.opts.ShowRateLimit = rateLimiter.IsEnabled()
	}

	// Main loop — each iteration creates a new container
mainLoop:
	for loopNum := 1; loopNum <= opts.MaxLoops; loopNum++ {
		// Check context cancellation
		if ctx.Err() != nil {
			result.ExitReason = "context cancelled"
			result.Error = ctx.Err()
			break
		}

		// Check circuit breaker
		if !circuit.Check() {
			result.ExitReason = circuit.TripReason()
			result.Error = fmt.Errorf("stagnation detected: %s", result.ExitReason)
			break
		}

		// Check rate limiter
		if rateLimiter.IsEnabled() && !rateLimiter.Allow() {
			opts.Logger.Warn().
				Int("limit", rateLimiter.Limit()).
				Time("reset_time", rateLimiter.ResetTime()).
				Msg("rate limit reached")

			if opts.Monitor != nil {
				fmt.Fprintln(opts.Monitor.opts.Writer, opts.Monitor.FormatRateLimitWait(rateLimiter.ResetTime()))
			}

			waitDuration := time.Until(rateLimiter.ResetTime())
			if waitDuration > 0 {
				select {
				case <-ctx.Done():
					result.ExitReason = "context cancelled while waiting for rate limit"
					result.Error = ctx.Err()
					break mainLoop
				case <-time.After(waitDuration):
					loopNum--
					continue
				}
			}
		}

		loopStart := time.Now()

		if opts.OnLoopStart != nil {
			opts.OnLoopStart(loopNum)
		}
		if opts.Monitor != nil {
			opts.Monitor.PrintLoopStart(loopNum)
		}

		// Create container for this iteration
		containerCfg, createErr := opts.CreateContainer(ctx)
		if createErr != nil {
			result.ExitReason = fmt.Sprintf("container creation failed: %v", createErr)
			result.Error = createErr
			result.LoopsCompleted = loopNum - 1
			break
		}

		opts.Logger.Debug().
			Int("loop", loopNum).
			Int("max_loops", opts.MaxLoops).
			Str("container", containerCfg.ContainerID).
			Msg("starting loop iteration")

		// Execute with timeout
		loopCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		text, resultEvent, exitCode, loopErr := r.StartContainer(loopCtx, opts.Logger, opts.ProjectCfg, containerCfg, opts.OnOutput, opts.OnStreamEvent)
		cancel()

		// Always cleanup the container after each iteration
		containerCfg.Cleanup()

		loopDuration := time.Since(loopStart)

		// Analyze stream result (text from TextAccumulator + ResultEvent metadata)
		analysis := AnalyzeStreamResult(text, resultEvent)
		status := analysis.Status
		result.FinalStatus = status
		result.LoopsCompleted = loopNum

		// Check for Claude's API rate limit
		if analysis.RateLimitHit {
			result.RateLimitHit = true
			opts.Logger.Warn().Msg("Claude API rate limit detected")

			if opts.Monitor != nil {
				isInteractive := opts.OnRateLimitHit != nil
				fmt.Fprintln(opts.Monitor.opts.Writer, opts.Monitor.FormatAPILimitError(isInteractive))
			}

			if opts.OnRateLimitHit != nil && opts.OnRateLimitHit() {
				select {
				case <-ctx.Done():
					result.ExitReason = "context cancelled while waiting for API rate limit"
					result.Error = ctx.Err()
					break mainLoop
				case <-time.After(60 * time.Second):
					loopNum--
					continue mainLoop
				}
			}

			result.ExitReason = "Claude API rate limit hit"
			result.Error = fmt.Errorf("claude API rate limit hit (5-hour limit)")
			break
		}

		// Update session
		session.Update(status, loopErr)
		rlState := rateLimiter.State()
		session.RateLimitState = &rlState
		if saveErr := r.store.SaveSession(session); saveErr != nil {
			opts.Logger.Error().Err(saveErr).Msg("failed to save session - progress may be lost")
			if opts.Monitor != nil {
				fmt.Fprintf(opts.Monitor.opts.Writer, "WARNING: Failed to save session: %v\n", saveErr)
			}
		} else {
			errStr := ""
			if loopErr != nil {
				errStr = loopErr.Error()
			}
			statusStr := ""
			if status != nil {
				statusStr = status.Status
			}
			if histErr := r.history.AddSessionEntry(opts.ProjectCfg.Name, opts.Agent, "updated", statusStr, errStr, session.LoopsCompleted); histErr != nil {
				opts.Logger.Warn().Err(histErr).Msg("failed to record session update in history")
			}
		}

		if opts.OnLoopEnd != nil {
			opts.OnLoopEnd(loopNum, status, resultEvent, loopErr)
		}
		if opts.Monitor != nil {
			opts.Monitor.PrintLoopEnd(loopNum, status, loopErr, analysis.OutputSize, loopDuration)
		}

		opts.Logger.Debug().
			Int("loop", loopNum).
			Int("exit_code", exitCode).
			Str("status", status.String()).
			Err(loopErr).
			Msg("completed loop iteration")

		// Check for completion using circuit breaker's analysis
		updateResult := circuit.UpdateWithAnalysis(status, analysis)

		// Surface repeated errors in history before they trip the circuit.
		if sameCount := circuit.SameErrorCount(); sameCount >= 3 && analysis != nil && analysis.ErrorSignature != "" {
			if histErr := r.history.AddSessionEntry(opts.ProjectCfg.Name, opts.Agent, "repeated_error", "", analysis.ErrorSignature, session.LoopsCompleted); histErr != nil {
				opts.Logger.Warn().Err(histErr).Msg("failed to record repeated error in history")
			}
		}

		if updateResult.IsComplete {
			result.ExitReason = "agent signaled completion"
			opts.Logger.Debug().Str("completion", updateResult.CompletionMsg).Msg("completion detected")
			break
		}

		if !opts.UseStrictCompletion && status != nil && status.IsComplete() {
			result.ExitReason = "agent signaled completion"
			break
		}

		if loopErr != nil {
			result.ExitReason = fmt.Sprintf("execution error: %v", loopErr)
			result.Error = loopErr
			break
		}

		if exitCode != 0 {
			opts.Logger.Warn().Int("exit_code", exitCode).Msg("non-zero exit code from claude")
		}

		if updateResult.Tripped {
			result.ExitReason = fmt.Sprintf("stagnation: %s", updateResult.Reason)
			result.Error = fmt.Errorf("circuit breaker tripped: %s", updateResult.Reason)

			now := time.Now()
			circuitState := &CircuitState{
				Project:         opts.ProjectCfg.Name,
				Agent:           opts.Agent,
				NoProgressCount: circuit.NoProgressCount(),
				Tripped:         true,
				TripReason:      updateResult.Reason,
				TrippedAt:       &now,
			}
			if saveErr := r.store.SaveCircuitState(circuitState); saveErr != nil {
				opts.Logger.Error().Err(saveErr).Msg("CRITICAL: failed to save circuit state")
				result.ExitReason = fmt.Sprintf("stagnation: %s (WARNING: circuit state not persisted)", updateResult.Reason)
				result.Error = fmt.Errorf("circuit breaker tripped (%s) and state save failed: %w", updateResult.Reason, saveErr)
			}

			state := circuit.State()
			if histErr := r.history.AddCircuitEntry(opts.ProjectCfg.Name, opts.Agent, "closed", "tripped", updateResult.Reason,
				state.NoProgressCount, state.SameErrorCount, state.ConsecutiveTestLoops, state.ConsecutiveCompletionCount); histErr != nil {
				opts.Logger.Warn().Err(histErr).Msg("failed to record circuit trip in history")
			}
			break
		}

		if opts.Monitor != nil {
			opts.Monitor.PrintLoopProgress(loopNum, status, circuit)
		}

		// Brief pause between loops
		loopDelay := time.Duration(opts.LoopDelaySeconds) * time.Second
		if loopDelay <= 0 {
			loopDelay = time.Duration(DefaultLoopDelaySeconds) * time.Second
		}
		select {
		case <-ctx.Done():
			result.ExitReason = "context cancelled"
			result.Error = ctx.Err()
			break mainLoop
		case <-time.After(loopDelay):
		}
	}

	// Check if we hit max loops
	if result.LoopsCompleted >= opts.MaxLoops && result.ExitReason == "" {
		result.ExitReason = "max loops reached"
		result.Error = fmt.Errorf("reached maximum loops (%d)", opts.MaxLoops)
	}

	if opts.Monitor != nil {
		opts.Monitor.PrintResult(result)
	}

	return result, nil
}

// StartContainer attaches to the container, starts it, parses stream-json
// output via io.Pipe + ParseStream, and returns the accumulated text,
// the terminal ResultEvent, the exit code, and any error.
//
// Streaming events are forwarded to onStreamEvent for rich TUI updates
// (tool starts, text deltas). Text deltas are also forwarded to onOutput
// for simple text display. Complete assistant messages are accumulated
// via TextAccumulator for LOOP_STATUS parsing.
func (r *Runner) StartContainer(ctx context.Context, log iostreams.Logger, projectCfg *config.Project, containerConfig *ContainerStartConfig, onOutput func([]byte), onStreamEvent func(*StreamDeltaEvent)) (string, *ResultEvent, int, error) {
	// Set up stream-json parsing pipeline: stdcopy → io.Pipe → ParseStream
	pr, pw := io.Pipe()
	textAcc, handler := NewTextAccumulator()

	// Forward streaming events for TUI display and/or text output
	if onOutput != nil || onStreamEvent != nil {
		handler.OnStreamEvent = func(e *StreamDeltaEvent) {
			if onStreamEvent != nil {
				onStreamEvent(e)
			}
			if onOutput != nil {
				if text := e.TextDelta(); text != "" {
					onOutput([]byte(text))
				}
			}
		}
	}

	// Attach to container BEFORE starting it
	attachOpts := docker.ContainerAttachOptions{
		Stream: true,
		Stdin:  false,
		Stdout: true,
		Stderr: true,
	}
	log.Debug().Msg("attaching to container before start")
	hijacked, err := r.client.ContainerAttach(ctx, containerConfig.ContainerID, attachOpts)
	if err != nil {
		pw.Close()
		log.Debug().Err(err).Msg("container attach failed")
		return "", nil, -1, fmt.Errorf("attaching to container: %w", err)
	}
	defer hijacked.Close()
	log.Debug().Msg("container attach succeeded")

	// Set up wait channel for container exit
	log.Debug().Msg("setting up container wait")
	statusCh := waitForContainerExit(ctx, log, r.client, containerConfig.ContainerID, false)

	// Start I/O streaming: stdcopy demuxes Docker's multiplexed stream.
	// Capture stderr (capped at 4KB) for diagnostics when Claude Code fails silently.
	var stderrBuf limitedBuffer
	stderrBuf.limit = 4096
	streamDone := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(pw, &stderrBuf, hijacked.Reader)
		pw.CloseWithError(err) // Signal EOF to ParseStream
		streamDone <- err
	}()

	// Start ParseStream in a goroutine — it reads from the pipe
	type parseResult struct {
		result *ResultEvent
		err    error
	}
	parseDone := make(chan parseResult, 1)
	go func() {
		resultEvent, parseErr := ParseStream(ctx, pr, handler)
		parseDone <- parseResult{resultEvent, parseErr}
	}()

	// Start the container
	log.Debug().Msg("starting container")
	if _, err := r.client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerConfig.ContainerID}); err != nil {
		pw.Close()
		return "", nil, -1, fmt.Errorf("start container failed: %w", err)
	}
	log.Debug().Msg("container started successfully")

	// Start socket bridge for GPG/SSH forwarding if needed
	if containershared.NeedsSocketBridge(projectCfg) {
		gpgEnabled := projectCfg.Security.GitCredentials != nil && projectCfg.Security.GitCredentials.GPGEnabled()
		// Socket bridge is accessed via the lifecycle config — the Runner no longer
		// holds a socketBridge field since it's wired through CreateContainer.
		// For now, log a warning if socket bridge is needed but not available.
		// The socket bridge setup is handled in the CreateContainer callback.
		log.Debug().Bool("gpg_enabled", gpgEnabled).Msg("socket bridge may be needed (handled by container factory)")
	}

	// Wait for stream completion and parse result
	var streamErr error
	select {
	case streamErr = <-streamDone:
		log.Debug().Err(streamErr).Msg("stream completed")
	case exitCode := <-statusCh:
		log.Debug().Int("exitCode", exitCode).Msg("container exited before stream completed")
		// Container exited — wait for stream to finish draining
		streamErr = <-streamDone
	}

	// Wait for ParseStream to complete
	parsed := <-parseDone
	text := textAcc.Text()

	// Diagnostic logging: what did ParseStream return?
	if parsed.result != nil {
		log.Debug().
			Str("subtype", parsed.result.Subtype).
			Bool("is_error", parsed.result.IsError).
			Int("num_turns", parsed.result.NumTurns).
			Float64("cost_usd", parsed.result.TotalCostUSD).
			Str("result_text", truncateForLog(parsed.result.Result, 200)).
			Int("num_errors", len(parsed.result.Errors)).
			Msg("ParseStream returned result event")
	} else if parsed.err == nil {
		log.Warn().Msg("ParseStream returned nil result AND nil error")
	}
	if stderr := stderrBuf.String(); stderr != "" {
		log.Debug().Str("stderr", truncateForLog(stderr, 500)).Msg("container stderr output")
	}
	log.Debug().Int("text_len", len(text)).Int("tool_calls", textAcc.ToolCallCount()).Msg("text accumulator state")

	// Determine exit code
	var exitCode int
	select {
	case code := <-statusCh:
		exitCode = code
	default:
		// Already consumed above or not yet available
		exitCode = 0
	}

	// Handle errors
	if parsed.err != nil && streamErr == nil {
		// ParseStream error but stream was clean — container exited without
		// producing a result event. Include captured stderr for diagnostics.
		stderr := stderrBuf.String()
		if stderr != "" {
			log.Warn().Err(parsed.err).Str("stderr", stderr).Msg("stream parse error with stderr output")
			return text, nil, exitCode, fmt.Errorf("%w (stderr: %s)", parsed.err, stderr)
		}
		log.Warn().Err(parsed.err).Msg("stream parse error (no stderr captured)")
		return text, nil, exitCode, parsed.err
	}
	if streamErr != nil {
		return text, parsed.result, -1, streamErr
	}
	if exitCode != 0 {
		return text, parsed.result, exitCode, &cmdutil.ExitError{Code: exitCode}
	}

	return text, parsed.result, exitCode, nil
}

// limitedBuffer is a bytes.Buffer that stops accepting writes after limit bytes.
// Used to capture stderr without unbounded memory growth.
type limitedBuffer struct {
	buf   []byte
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.buf)
	if remaining <= 0 {
		return len(p), nil // discard but report success
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.buf)
}

// truncateForLog trims a string for safe inclusion in log fields.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ResetCircuit resets the circuit breaker for a project/agent.
func (r *Runner) ResetCircuit(project, agent string) error {
	return r.store.DeleteCircuitState(project, agent)
}

// ResetSession resets both session and circuit for a project/agent.
func (r *Runner) ResetSession(project, agent string) error {
	if err := r.store.DeleteSession(project, agent); err != nil {
		return err
	}
	return r.store.DeleteCircuitState(project, agent)
}

// GetSession returns the current session for a project/agent.
func (r *Runner) GetSession(project, agent string) (*Session, error) {
	return r.store.LoadSession(project, agent)
}

// GetCircuitState returns the circuit breaker state for a project/agent.
func (r *Runner) GetCircuitState(project, agent string) (*CircuitState, error) {
	return r.store.LoadCircuitState(project, agent)
}

// waitForContainerExit sets up a channel that receives the container's exit status code.
func waitForContainerExit(ctx context.Context, log iostreams.Logger, client *docker.Client, containerID string, autoRemove bool) <-chan int {
	condition := container.WaitConditionNextExit
	if autoRemove {
		condition = container.WaitConditionRemoved
	}

	statusCh := make(chan int, 1)
	go func() {
		defer close(statusCh)
		waitResult := client.ContainerWait(ctx, containerID, condition)
		select {
		case <-ctx.Done():
			return
		case result := <-waitResult.Result:
			if result.Error != nil {
				log.Error().Str("message", result.Error.Message).Msg("container wait error")
				statusCh <- 125
			} else {
				statusCh <- int(result.StatusCode)
			}
		case err := <-waitResult.Error:
			log.Error().Err(err).Msg("error waiting for container")
			statusCh <- 125
		}
	}()
	return statusCh
}
