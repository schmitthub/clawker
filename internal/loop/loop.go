package loop

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
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

// Options configures the loop execution.
type Options struct {
	// ContainerName is the full container name (clawker.project.agent).
	ContainerName string

	// Project is the project name.
	Project string

	// Agent is the agent name.
	Agent string

	// Prompt is the initial prompt (used only on first loop if provided).
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

	// Monitor is the optional monitor for live output.
	Monitor *Monitor

	// Verbose enables verbose logging.
	Verbose bool

	// OnLoopStart is called before each loop iteration.
	OnLoopStart func(loopNum int)

	// OnLoopEnd is called after each loop iteration.
	OnLoopEnd func(loopNum int, status *Status, err error)

	// OnOutput is called with output chunks during execution.
	OnOutput func(chunk []byte)

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

// NewRunner creates a new Runner with the given Docker client.
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
func (r *Runner) Run(ctx context.Context, opts Options) (*Result, error) {
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
	session, expired, err := r.store.LoadSessionWithExpiration(opts.Project, opts.Agent, opts.SessionExpirationHours)
	if err != nil {
		logger.Error().Err(err).Msg("failed to load session")
		return &Result{
			ExitReason: "failed to load session",
			Error:      fmt.Errorf("failed to load session (use --reset-circuit --all to start fresh): %w", err),
		}, nil
	}
	if expired {
		logger.Info().Msg("session expired, starting fresh")
		// Record session expiration in history
		if histErr := r.history.AddSessionEntry(opts.Project, opts.Agent, "expired", "", "", 0); histErr != nil {
			logger.Warn().Err(histErr).Msg("failed to record session expiration in history")
		}
	}
	sessionCreated := session == nil
	if session == nil {
		session = NewSession(opts.Project, opts.Agent, opts.Prompt)
	}
	// Record session creation in history and save session immediately
	// This ensures `loop status` can see the session before the first loop completes
	if sessionCreated {
		if histErr := r.history.AddSessionEntry(opts.Project, opts.Agent, "created", StatusPending, "", 0); histErr != nil {
			logger.Warn().Err(histErr).Msg("failed to record session creation in history")
		}
		// Save session immediately so status command can see it
		if saveErr := r.store.SaveSession(session); saveErr != nil {
			logger.Error().Err(saveErr).Msg("failed to save initial session")
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
			logger.Warn().Msg("rate limit state expired or invalid, starting fresh window")
		}
	}

	// Initialize circuit breaker with full config
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
		if err := r.store.DeleteCircuitState(opts.Project, opts.Agent); err != nil {
			logger.Warn().Err(err).Msg("failed to delete circuit state")
			return &Result{
				Session:    session,
				ExitReason: "failed to reset circuit breaker",
				Error:      fmt.Errorf("failed to reset circuit breaker as requested: %w", err),
			}, nil
		}
		// Record circuit reset in history
		if histErr := r.history.AddCircuitEntry(opts.Project, opts.Agent, "tripped", "closed", "manual reset", 0, 0, 0, 0); histErr != nil {
			logger.Warn().Err(histErr).Msg("failed to record circuit reset in history")
		}
	} else {
		// Load existing circuit state
		circuitState, err := r.store.LoadCircuitState(opts.Project, opts.Agent)
		if err != nil {
			logger.Error().Err(err).Msg("failed to load circuit state - refusing to run")
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

	// Main loop
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
			logger.Warn().
				Int("limit", rateLimiter.Limit()).
				Time("reset_time", rateLimiter.ResetTime()).
				Msg("rate limit reached")

			if opts.Monitor != nil {
				fmt.Fprintln(opts.Monitor.opts.Writer, opts.Monitor.FormatRateLimitWait(rateLimiter.ResetTime()))
			}

			// Wait until reset time
			waitDuration := time.Until(rateLimiter.ResetTime())
			if waitDuration > 0 {
				select {
				case <-ctx.Done():
					result.ExitReason = "context cancelled while waiting for rate limit"
					result.Error = ctx.Err()
					break mainLoop
				case <-time.After(waitDuration):
					// Rate limit should be reset now, retry this loop
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

		logger.Info().
			Int("loop", loopNum).
			Int("max_loops", opts.MaxLoops).
			Str("container", opts.ContainerName).
			Msg("starting loop iteration")

		// Build command
		var cmd []string
		if loopNum == 1 && opts.Prompt != "" {
			cmd = []string{"claude", "-p", opts.Prompt}
		} else {
			cmd = []string{"claude", "--continue"}
		}
		if opts.SkipPermissions {
			cmd = append(cmd, "--dangerously-skip-permissions")
		}

		// Execute with timeout
		loopCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		output, exitCode, loopErr := r.ExecCapture(loopCtx, opts.ContainerName, cmd, opts.OnOutput)
		cancel()

		loopDuration := time.Since(loopStart)

		// Full analysis of output
		analysis := AnalyzeOutput(output)
		status := analysis.Status
		result.FinalStatus = status
		result.LoopsCompleted = loopNum

		// Check for Claude's API rate limit
		if analysis.RateLimitHit {
			result.RateLimitHit = true
			logger.Warn().Msg("Claude API rate limit detected")

			if opts.Monitor != nil {
				isInteractive := opts.OnRateLimitHit != nil
				fmt.Fprintln(opts.Monitor.opts.Writer, opts.Monitor.FormatAPILimitError(isInteractive))
			}

			if opts.OnRateLimitHit != nil && opts.OnRateLimitHit() {
				// User chose to wait, sleep for a bit and retry
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
			logger.Error().Err(saveErr).Msg("failed to save session - progress may be lost")
			if opts.Monitor != nil {
				fmt.Fprintf(opts.Monitor.opts.Writer, "WARNING: Failed to save session: %v\n", saveErr)
			}
		} else {
			// Record session update in history
			errStr := ""
			if loopErr != nil {
				errStr = loopErr.Error()
			}
			statusStr := ""
			if status != nil {
				statusStr = status.Status
			}
			if histErr := r.history.AddSessionEntry(opts.Project, opts.Agent, "updated", statusStr, errStr, session.LoopsCompleted); histErr != nil {
				logger.Warn().Err(histErr).Msg("failed to record session update in history")
			}
		}

		if opts.OnLoopEnd != nil {
			opts.OnLoopEnd(loopNum, status, loopErr)
		}
		if opts.Monitor != nil {
			opts.Monitor.PrintLoopEnd(loopNum, status, loopErr, analysis.OutputSize, loopDuration)
		}

		logger.Info().
			Int("loop", loopNum).
			Int("exit_code", exitCode).
			Str("status", status.String()).
			Err(loopErr).
			Msg("completed loop iteration")

		// Check for completion using circuit breaker's analysis
		updateResult := circuit.UpdateWithAnalysis(status, analysis)

		if updateResult.IsComplete {
			result.ExitReason = "agent signaled completion"
			logger.Info().Str("completion", updateResult.CompletionMsg).Msg("completion detected")
			break
		}

		// For backward compatibility: check basic completion if strict mode not required
		if !opts.UseStrictCompletion && status != nil && status.IsComplete() {
			result.ExitReason = "agent signaled completion"
			break
		}

		// Check for execution error
		if loopErr != nil {
			result.ExitReason = fmt.Sprintf("execution error: %v", loopErr)
			result.Error = loopErr
			break
		}

		// Check for non-zero exit code
		if exitCode != 0 {
			logger.Warn().Int("exit_code", exitCode).Msg("non-zero exit code from claude")
		}

		// Check if circuit tripped from update
		if updateResult.Tripped {
			result.ExitReason = fmt.Sprintf("stagnation: %s", updateResult.Reason)
			result.Error = fmt.Errorf("circuit breaker tripped: %s", updateResult.Reason)

			// Save circuit state
			now := time.Now()
			circuitState := &CircuitState{
				Project:         opts.Project,
				Agent:           opts.Agent,
				NoProgressCount: circuit.NoProgressCount(),
				Tripped:         true,
				TripReason:      updateResult.Reason,
				TrippedAt:       &now,
			}
			if saveErr := r.store.SaveCircuitState(circuitState); saveErr != nil {
				logger.Error().Err(saveErr).Msg("CRITICAL: failed to save circuit state")
				result.ExitReason = fmt.Sprintf("stagnation: %s (WARNING: circuit state not persisted)", updateResult.Reason)
				result.Error = fmt.Errorf("circuit breaker tripped (%s) and state save failed: %w", updateResult.Reason, saveErr)
			}

			// Record circuit trip in history
			state := circuit.State()
			if histErr := r.history.AddCircuitEntry(opts.Project, opts.Agent, "closed", "tripped", updateResult.Reason,
				state.NoProgressCount, state.SameErrorCount, state.ConsecutiveTestLoops, state.ConsecutiveCompletionCount); histErr != nil {
				logger.Warn().Err(histErr).Msg("failed to record circuit trip in history")
			}
			break
		}

		// Monitor progress output
		if opts.Monitor != nil {
			opts.Monitor.PrintLoopProgress(loopNum, status, circuit)
		}

		// Brief pause between loops
		loopDelay := time.Duration(opts.LoopDelaySeconds) * time.Second
		if loopDelay <= 0 {
			loopDelay = time.Duration(DefaultLoopDelaySeconds) * time.Second
		}
		time.Sleep(loopDelay)
	}

	// Check if we hit max loops
	if result.LoopsCompleted >= opts.MaxLoops && result.ExitReason == "" {
		result.ExitReason = "max loops reached"
		result.Error = fmt.Errorf("reached maximum loops (%d)", opts.MaxLoops)
	}

	// Print final result if monitor available
	if opts.Monitor != nil {
		opts.Monitor.PrintResult(result)
	}

	return result, nil
}

// ExecCapture executes a command in the container and captures output.
func (r *Runner) ExecCapture(ctx context.Context, containerName string, cmd []string, onOutput func([]byte)) (string, int, error) {
	// Find container
	c, err := r.client.FindContainerByName(ctx, containerName)
	if err != nil {
		return "", -1, fmt.Errorf("failed to find container: %w", err)
	}
	if c.State != "running" {
		return "", -1, fmt.Errorf("container %q is not running", containerName)
	}

	// Create exec
	execConfig := docker.ExecCreateOptions{
		AttachStdin:  false,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          false,
		Cmd:          cmd,
	}

	execResp, err := r.client.ExecCreate(ctx, c.ID, execConfig)
	if err != nil {
		return "", -1, fmt.Errorf("failed to create exec: %w", err)
	}

	// Attach to exec
	hijacked, err := r.client.ExecAttach(ctx, execResp.ID, docker.ExecAttachOptions{TTY: false})
	if err != nil {
		return "", -1, fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer hijacked.Close()

	// Capture output
	var stdout, stderr bytes.Buffer
	var outputWriter io.Writer = &stdout

	// If we have a callback, wrap the writer
	if onOutput != nil {
		outputWriter = io.MultiWriter(&stdout, &callbackWriter{fn: onOutput})
	}

	// Run StdCopy in a goroutine so we can respect context cancellation
	// When context is cancelled, we close the connection to interrupt the read
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(outputWriter, &stderr, hijacked.Reader)
		copyDone <- copyErr
	}()

	// Wait for either copy completion or context cancellation
	var copyErr error
	select {
	case copyErr = <-copyDone:
		// Normal completion
	case <-ctx.Done():
		// Context cancelled/timeout - close connection to unblock StdCopy
		hijacked.Close()
		<-copyDone // Wait for goroutine to finish
		return stdout.String(), -1, fmt.Errorf("exec timed out: %w", ctx.Err())
	}

	if copyErr != nil && copyErr != io.EOF {
		return stdout.String(), -1, fmt.Errorf("failed to read output: %w", copyErr)
	}

	// Get exit code - use fresh context since the loop context may have timed out
	// but we still need to retrieve the exit code from the completed exec
	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer inspectCancel()
	inspectResp, err := r.client.ExecInspect(inspectCtx, execResp.ID, docker.ExecInspectOptions{})
	if err != nil {
		return stdout.String(), -1, fmt.Errorf("failed to inspect exec: %w", err)
	}

	// Log stderr separately if non-empty (Fix #5: don't silently merge)
	output := stdout.String()
	if stderr.Len() > 0 {
		logger.Debug().Str("stderr", stderr.String()).Msg("claude stderr output")
		output += "\n" + stderr.String()
	}

	return output, inspectResp.ExitCode, nil
}

// callbackWriter wraps a callback function as an io.Writer.
type callbackWriter struct {
	fn func([]byte)
}

func (w *callbackWriter) Write(p []byte) (n int, err error) {
	w.fn(p)
	return len(p), nil
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
