package ralph

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

// LoopResult represents the outcome of running the Ralph loop.
type LoopResult struct {
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
}

// LoopOptions configures the Ralph loop execution.
type LoopOptions struct {
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

	// OnLoopStart is called before each loop iteration.
	OnLoopStart func(loopNum int)

	// OnLoopEnd is called after each loop iteration.
	OnLoopEnd func(loopNum int, status *Status, err error)

	// OnOutput is called with output chunks during execution.
	OnOutput func(chunk []byte)
}

// Runner executes Ralph loops.
type Runner struct {
	client *docker.Client
	store  *SessionStore
}

// NewRunner creates a new Runner with the given Docker client.
func NewRunner(client *docker.Client) (*Runner, error) {
	store, err := DefaultSessionStore()
	if err != nil {
		return nil, err
	}
	return &Runner{
		client: client,
		store:  store,
	}, nil
}

// Run executes the Ralph loop until completion, error, or max loops.
func (r *Runner) Run(ctx context.Context, opts LoopOptions) (*LoopResult, error) {
	// Set defaults
	if opts.MaxLoops <= 0 {
		opts.MaxLoops = 50
	}
	if opts.StagnationThreshold <= 0 {
		opts.StagnationThreshold = 3
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Minute
	}

	// Load or create session
	session, err := r.store.LoadSession(opts.Project, opts.Agent)
	if err != nil {
		// Distinguish between "no session" (nil, nil) and "load error"
		logger.Error().Err(err).Msg("failed to load existing session - starting fresh but data may be lost")
	}
	if session == nil {
		session = NewSession(opts.Project, opts.Agent, opts.Prompt)
	}

	// Initialize circuit breaker
	circuit := NewCircuitBreaker(opts.StagnationThreshold)

	// Reset circuit if requested
	if opts.ResetCircuit {
		if err := r.store.DeleteCircuitState(opts.Project, opts.Agent); err != nil {
			logger.Warn().Err(err).Msg("failed to delete circuit state")
		}
	} else {
		// Load existing circuit state
		circuitState, err := r.store.LoadCircuitState(opts.Project, opts.Agent)
		if err != nil {
			// Error loading circuit state is critical - it may be tripped and we don't know
			logger.Error().Err(err).Msg("failed to load circuit state - refusing to run")
			return &LoopResult{
				Session:    session,
				ExitReason: "failed to load circuit state",
				Error:      fmt.Errorf("failed to load circuit state (may be tripped): %w", err),
			}, nil
		}
		if circuitState != nil && circuitState.Tripped {
			return &LoopResult{
				Session:    session,
				ExitReason: fmt.Sprintf("circuit already tripped: %s", circuitState.TripReason),
				Error:      fmt.Errorf("circuit breaker tripped: %s", circuitState.TripReason),
			}, nil
		}
	}

	result := &LoopResult{
		Session: session,
	}

	// Main loop
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

		if opts.OnLoopStart != nil {
			opts.OnLoopStart(loopNum)
		}

		logger.Info().
			Int("loop", loopNum).
			Int("max_loops", opts.MaxLoops).
			Str("container", opts.ContainerName).
			Msg("starting ralph loop")

		// Build command
		var cmd []string
		if loopNum == 1 && opts.Prompt != "" {
			cmd = []string{"claude", "-p", opts.Prompt}
		} else {
			cmd = []string{"claude", "--continue"}
		}

		// Execute with timeout
		loopCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		output, exitCode, loopErr := r.execCapture(loopCtx, opts.ContainerName, cmd, opts.OnOutput)
		cancel()

		// Parse status
		status := ParseStatus(output)
		result.FinalStatus = status
		result.LoopsCompleted = loopNum

		// Update session
		session.Update(status, loopErr)
		if saveErr := r.store.SaveSession(session); saveErr != nil {
			logger.Error().Err(saveErr).Msg("failed to save session - progress may be lost")
		}

		if opts.OnLoopEnd != nil {
			opts.OnLoopEnd(loopNum, status, loopErr)
		}

		logger.Info().
			Int("loop", loopNum).
			Int("exit_code", exitCode).
			Str("status", status.String()).
			Err(loopErr).
			Msg("completed ralph loop")

		// Check for completion
		if status != nil && status.IsComplete() {
			result.ExitReason = "agent signaled completion"
			break
		}

		// Check for execution error
		if loopErr != nil {
			result.ExitReason = fmt.Sprintf("execution error: %v", loopErr)
			result.Error = loopErr
			break
		}

		// Check for non-zero exit code (might indicate issue)
		if exitCode != 0 {
			logger.Warn().Int("exit_code", exitCode).Msg("non-zero exit code from claude")
		}

		// Update circuit breaker
		if tripped, reason := circuit.Update(status); tripped {
			result.ExitReason = fmt.Sprintf("stagnation: %s", reason)
			result.Error = fmt.Errorf("circuit breaker tripped: %s", reason)

			// Save circuit state
			now := time.Now()
			circuitState := &CircuitState{
				Project:         opts.Project,
				Agent:           opts.Agent,
				NoProgressCount: circuit.NoProgressCount(),
				Tripped:         true,
				TripReason:      reason,
				TrippedAt:       &now,
			}
			if saveErr := r.store.SaveCircuitState(circuitState); saveErr != nil {
				logger.Error().Err(saveErr).Msg("CRITICAL: failed to save circuit state")
				// Include warning in exit reason so user knows circuit state wasn't persisted
				result.ExitReason = fmt.Sprintf("stagnation: %s (WARNING: circuit state not persisted)", reason)
			}
			break
		}

		// Brief pause between loops
		time.Sleep(time.Second)
	}

	// Check if we hit max loops
	if result.LoopsCompleted >= opts.MaxLoops && result.ExitReason == "" {
		result.ExitReason = "max loops reached"
		result.Error = fmt.Errorf("reached maximum loops (%d)", opts.MaxLoops)
	}

	return result, nil
}

// execCapture executes a command in the container and captures output.
func (r *Runner) execCapture(ctx context.Context, containerName string, cmd []string, onOutput func([]byte)) (string, int, error) {
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

	_, err = stdcopy.StdCopy(outputWriter, &stderr, hijacked.Reader)
	if err != nil && err != io.EOF {
		return stdout.String(), -1, fmt.Errorf("failed to read output: %w", err)
	}

	// Get exit code
	inspectResp, err := r.client.ExecInspect(ctx, execResp.ID, docker.ExecInspectOptions{})
	if err != nil {
		return stdout.String(), -1, fmt.Errorf("failed to inspect exec: %w", err)
	}

	// Combine stdout and stderr for output
	output := stdout.String()
	if stderr.Len() > 0 {
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

// GetSession returns the current session for a project/agent.
func (r *Runner) GetSession(project, agent string) (*Session, error) {
	return r.store.LoadSession(project, agent)
}

// GetCircuitState returns the circuit breaker state for a project/agent.
func (r *Runner) GetCircuitState(project, agent string) (*CircuitState, error) {
	return r.store.LoadCircuitState(project, agent)
}
