package loop

import (
	"bytes"
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
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
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

type ContainerStartConfig struct {
	ContainerID string
	Cleanup     func()
}

type ContainerAttachConfig struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Options configures the loop execution.
type Options struct {
	// ContainerName is the full container name (clawker.project.agent).
	ContainerStartConfig *ContainerStartConfig

	// ProjectCfg is the project name.
	ProjectCfg *config.Project

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

	// SystemPrompt is the full system prompt appended via --append-system-prompt.
	// Built by BuildSystemPrompt() which combines the default LOOP_STATUS
	// instructions with any user-provided additional instructions.
	SystemPrompt string

	// Monitor is the optional monitor for live output.
	Monitor *Monitor

	// WorkDir is the host working directory for this session.
	WorkDir string

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
	client       *docker.Client
	socketBridge func() socketbridge.SocketBridgeManager
	store        *SessionStore
	history      *HistoryStore
}

// NewRunner creates a new Runner with the given Docker client.
func NewRunner(f *cmdutil.Factory) (*Runner, error) {
	store, err := DefaultSessionStore()
	if err != nil {
		return nil, err
	}
	history, err := DefaultHistoryStore()
	if err != nil {
		return nil, err
	}
	client, err := f.Client(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	return &Runner{
		client:       client,
		socketBridge: f.SocketBridge,
		store:        store,
		history:      history,
	}, nil
}

// NewRunnerWith creates a Runner with explicit store and history dependencies.
// This is useful for testing with custom storage directories.
func NewRunnerWith(f *cmdutil.Factory, store *SessionStore, history *HistoryStore) (*Runner, error) {
	client, err := f.Client(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	return &Runner{
		client:       client,
		socketBridge: f.SocketBridge,
		store:        store,
		history:      history,
	}, nil
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
	session, expired, err := r.store.LoadSessionWithExpiration(opts.ProjectCfg.Project, opts.Agent, opts.SessionExpirationHours)
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
		if histErr := r.history.AddSessionEntry(opts.ProjectCfg.Project, opts.Agent, "expired", "", "", 0); histErr != nil {
			logger.Warn().Err(histErr).Msg("failed to record session expiration in history")
		}
	}
	sessionCreated := session == nil
	if session == nil {
		session = NewSession(opts.ProjectCfg.Project, opts.Agent, opts.Prompt, opts.WorkDir)
	}
	// Record session creation in history and save session immediately
	// This ensures `loop status` can see the session before the first loop completes
	if sessionCreated {
		if histErr := r.history.AddSessionEntry(opts.ProjectCfg.Project, opts.Agent, "created", StatusPending, "", 0); histErr != nil {
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
		if err := r.store.DeleteCircuitState(opts.ProjectCfg.Project, opts.Agent); err != nil {
			logger.Warn().Err(err).Msg("failed to delete circuit state")
			return &Result{
				Session:    session,
				ExitReason: "failed to reset circuit breaker",
				Error:      fmt.Errorf("failed to reset circuit breaker as requested: %w", err),
			}, nil
		}
		// Record circuit reset in history
		if histErr := r.history.AddCircuitEntry(opts.ProjectCfg.Project, opts.Agent, "tripped", "closed", "manual reset", 0, 0, 0, 0); histErr != nil {
			logger.Warn().Err(histErr).Msg("failed to record circuit reset in history")
		}
	} else {
		// Load existing circuit state
		circuitState, err := r.store.LoadCircuitState(opts.ProjectCfg.Project, opts.Agent)
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
			Str("container", opts.ContainerStartConfig.ContainerID).
			Msg("starting loop iteration")

		// Execute with timeout
		loopCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		output, exitCode, loopErr := r.StartContainer(loopCtx, opts.ProjectCfg, opts.ContainerStartConfig, opts.OnOutput)
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
			if histErr := r.history.AddSessionEntry(opts.ProjectCfg.Project, opts.Agent, "updated", statusStr, errStr, session.LoopsCompleted); histErr != nil {
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

		// Surface repeated errors in history before they trip the circuit.
		// This makes same-error patterns visible in `loop status` early.
		if sameCount := circuit.SameErrorCount(); sameCount >= 3 && analysis != nil && analysis.ErrorSignature != "" {
			if histErr := r.history.AddSessionEntry(opts.ProjectCfg.Project, opts.Agent, "repeated_error", "", analysis.ErrorSignature, session.LoopsCompleted); histErr != nil {
				logger.Warn().Err(histErr).Msg("failed to record repeated error in history")
			}
		}

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
				Project:         opts.ProjectCfg.Project,
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
			if histErr := r.history.AddCircuitEntry(opts.ProjectCfg.Project, opts.Agent, "closed", "tripped", updateResult.Reason,
				state.NoProgressCount, state.SameErrorCount, state.ConsecutiveTestLoops, state.ConsecutiveCompletionCount); histErr != nil {
				logger.Warn().Err(histErr).Msg("failed to record circuit trip in history")
			}
			break
		}

		// Monitor progress output
		if opts.Monitor != nil {
			opts.Monitor.PrintLoopProgress(loopNum, status, circuit)
		}

		// Brief pause between loops (context-aware so Ctrl+C is responsive)
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

	// Print final result if monitor available
	if opts.Monitor != nil {
		opts.Monitor.PrintResult(result)
	}

	return result, nil
}

// StartContainer starts the container and captures output.
func (r *Runner) StartContainer(ctx context.Context, projectCfg *config.Project, containerConfig *ContainerStartConfig, onOutput func([]byte)) (string, int, error) {

	// capture output in a buffer while also forwarding to onOutput callback
	var stdout, stderr bytes.Buffer
	var outputWriter io.Writer = &stdout

	if onOutput != nil {
		outputWriter = io.MultiWriter(&stdout, &callbackWriter{fn: onOutput})
	}

	// Attach to container BEFORE starting it
	// This is critical for short-lived containers (especially with --rm) where the container
	// might exit and be removed before we can attach if we start first.
	attachOpts := docker.ContainerAttachOptions{
		Stream: true,
		Stdin:  false,
		Stdout: true,
		Stderr: true,
	}
	logger.Debug().Msg("attaching to container before start")
	hijacked, err := r.client.ContainerAttach(ctx, containerConfig.ContainerID, attachOpts)
	if err != nil {
		logger.Debug().Err(err).Msg("container attach failed")
		return stdout.String(), -1, fmt.Errorf("attaching to container: %w", err)
	}
	defer hijacked.Close()
	logger.Debug().Msg("container attach succeeded")

	// Set up wait channel for container exit following Docker CLI's waitExitOrRemoved pattern.
	// This wraps the dual-channel ContainerWait into a single status channel.
	// Must use WaitConditionNextExit (not WaitConditionNotRunning) because this is called
	// before the container starts — a "created" container is already not-running.
	logger.Debug().Msg("setting up container wait")
	statusCh := waitForContainerExit(ctx, r.client, containerConfig.ContainerID, false)

	// Start I/O streaming BEFORE starting the container.
	// This ensures we're ready to receive output immediately when the container starts.
	// Following Docker CLI pattern: I/O goroutines start pre-start, resize happens post-start.
	streamDone := make(chan error, 1)

	go func() {
		_, err := stdcopy.StdCopy(outputWriter, &stderr, hijacked.Reader)
		streamDone <- err
	}()

	// Now start the container — the I/O streaming goroutines are already running
	logger.Debug().Msg("starting container ")
	if _, err := r.client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerConfig.ContainerID}); err != nil {
		containerConfig.Cleanup()
		return stdout.String(), -1, fmt.Errorf("start container failed: %w", err)
	}
	logger.Debug().Msg("container started successfully")

	// Start socket bridge for GPG/SSH forwarding if needed.
	if containershared.NeedsSocketBridge(projectCfg) && r.socketBridge != nil {
		gpgEnabled := projectCfg.Security.GitCredentials != nil && projectCfg.Security.GitCredentials.GPGEnabled()
		if err := r.socketBridge().EnsureBridge(containerConfig.ContainerID, gpgEnabled); err != nil {
			logger.Warn().Err(err).Msg("failed to start socket bridge for loop container")
			return stdout.String(), -1, fmt.Errorf("socket bridge failed: %w (GPG/SSH forwarding may not work)", err)
		}
	}

	// Wait for stream completion or container exit.
	// Following Docker CLI's run.go pattern: when stream ends, check exit status;
	// when exit status arrives first, drain the stream.
	select {
	case err := <-streamDone:
		logger.Debug().Err(err).Msg("stream completed")
		if err != nil {
			return stdout.String(), -1, err
		}
		// Stream done — check for container exit status.
		// For normal container exits, the status is available almost immediately.
		// For detach (Ctrl+P Ctrl+Q), the container is still running so no status
		// arrives. We use a timeout to distinguish the two cases without blocking
		// forever. This is necessary because we don't do client-side detach key
		// detection (Docker CLI uses term.EscapeError for this).
		select {
		case status := <-statusCh:
			logger.Debug().Int("exitCode", status).Msg("container exited")
			if status != 0 {
				return "", -1, &cmdutil.ExitError{Code: status}
			}
			return stdout.String(), status, nil
		case <-time.After(2 * time.Second):
			// No exit status within timeout — stream ended due to detach, not exit.
			logger.Debug().Msg("no exit status received after stream ended, assuming detach")
			return "", -1, nil
		}
	case status := <-statusCh:
		logger.Debug().Int("exitCode", status).Msg("container exited before stream completed")
		if status != 0 {
			return stdout.String(), -1, &cmdutil.ExitError{Code: status}
		}
	}

	return stdout.String(), 0, nil
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

// waitForContainerExit sets up a channel that receives the container's exit status code.
// It follows Docker CLI's waitExitOrRemoved pattern:
//   - Uses WaitConditionNextExit (not WaitConditionNotRunning) so it can be called
//     BEFORE the container starts without returning immediately for "created" containers.
//   - Uses WaitConditionRemoved when autoRemove is true (--rm) so the wait doesn't fail
//     when the container is removed after exit.
func waitForContainerExit(ctx context.Context, client *docker.Client, containerID string, autoRemove bool) <-chan int {
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
				logger.Error().Str("message", result.Error.Message).Msg("container wait error")
				statusCh <- 125
			} else {
				statusCh <- int(result.StatusCode)
			}
		case err := <-waitResult.Error:
			logger.Error().Err(err).Msg("error waiting for container")
			statusCh <- 125
		}
	}()
	return statusCh
}
