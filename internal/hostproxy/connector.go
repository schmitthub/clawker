package hostproxy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"

	hostproxyv1 "github.com/schmitthub/clawker/api/hostproxy/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

// Reconnect backoff. The wait doubles after each failed or short stream
// up to maxBackoff, and starts again from minBackoff after a stream that
// stayed up for backoffResetAfter.
const (
	defaultMinBackoff = 500 * time.Millisecond
	defaultMaxBackoff = 30 * time.Second
	backoffResetAfter = time.Minute
	backoffFactor     = 2
)

// Dialer opens a connection to the CP admin listener. The connector calls
// it for every stream attempt, so the identity it loads (CLI CA, host
// proxy client leaf, Hydra signing key) is re-read from disk each time
// and `clawker auth rotate` takes effect on the next reconnect without a
// daemon restart.
type Dialer func() (*grpc.ClientConn, error)

// Connector holds the host proxy's HostProxyService stream to the CP. The
// CP sends authorized host actions down the stream; the connector runs
// each one on Actions and sends back the result. When the stream ends (CP
// restart, network fault, a newer host proxy replaced it), the connector
// dials again and opens a new one with backoff.
type Connector struct {
	dial    Dialer
	actions *Actions
	log     *logger.Logger

	minBackoff time.Duration
	maxBackoff time.Duration
}

// NewConnector returns a Connector that serves actions over connections
// from dial.
func NewConnector(dial Dialer, actions *Actions, log *logger.Logger) *Connector {
	return &Connector{
		dial:       dial,
		actions:    actions,
		log:        log,
		minBackoff: defaultMinBackoff,
		maxBackoff: defaultMaxBackoff,
	}
}

// Run serves the CP stream until ctx ends.
func (c *Connector) Run(ctx context.Context) {
	backoff := c.minBackoff
	for {
		start := time.Now()
		accepted, err := c.serve(ctx)
		if ctx.Err() != nil {
			return
		}
		if accepted && time.Since(start) >= backoffResetAfter {
			backoff = c.minBackoff
		}
		c.log.Warn().Err(err).
			Str("event", "host_proxy_stream_ended").
			Bool("accepted", accepted).
			Dur("retry_in", backoff).
			Msg("host proxy stream to the control plane ended; reconnecting")
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = min(backoff*backoffFactor, c.maxBackoff)
	}
}

// serve dials the CP, opens one stream and serves it until it ends. The
// bool reports whether the CP accepted the stream.
func (c *Connector) serve(ctx context.Context) (bool, error) {
	conn, err := c.dial()
	if err != nil {
		return false, fmt.Errorf("dial control plane: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			c.log.Debug().Err(closeErr).Msg("close control plane connection")
		}
	}()

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := hostproxyv1.NewHostProxyServiceClient(conn).Connect(streamCtx)
	if err != nil {
		return false, fmt.Errorf("open stream: %w", err)
	}
	md, err := stream.Header()
	if err != nil {
		return false, fmt.Errorf("stream header: %w", err)
	}
	if len(md.Get(hostproxyv1.AcceptedHeader)) == 0 {
		// A refused stream ends trailers-only; Recv returns its status.
		_, err = stream.Recv()
		return false, fmt.Errorf("control plane refused the stream: %w", err)
	}
	c.log.Info().Str("event", "host_proxy_connected").Msg("host proxy connected to the control plane")
	return true, c.serveStream(streamCtx, cancel, stream)
}

// serveStream runs each action the CP sends until the stream ends. cancel
// ends the stream context so actions in flight stop before it returns.
func (c *Connector) serveStream(
	streamCtx context.Context, cancel context.CancelFunc, stream hostproxyv1.HostProxyService_ConnectClient,
) error {
	var (
		sendMu   sync.Mutex // gRPC allows one Send at a time
		wg       sync.WaitGroup
		inflight = newInflight()
	)
	for {
		req, recvErr := stream.Recv()
		if recvErr != nil {
			// Cancel first: a git helper in flight is killed, then wait.
			cancel()
			wg.Wait()
			return fmt.Errorf("receive from control plane: %w", recvErr)
		}
		if req.GetCancel() != nil {
			// The CP stopped waiting for this id; end the action, send nothing.
			inflight.cancel(req.GetId())
			continue
		}
		actx, acancel := inflight.start(streamCtx, req)
		wg.Go(func() {
			defer acancel()
			defer inflight.done(req.GetId())
			res := c.actions.Handle(actx, req)
			sendMu.Lock()
			sendErr := stream.Send(res)
			sendMu.Unlock()
			if sendErr != nil {
				c.log.Debug().Err(sendErr).Uint64("id", req.GetId()).Msg("send host action result")
			}
		})
	}
}

// inflightActions maps the id of each running action to its cancel func,
// so a CP cancel notice can end it.
type inflightActions struct {
	mu      sync.Mutex
	cancels map[uint64]context.CancelFunc
}

func newInflight() *inflightActions {
	return &inflightActions{mu: sync.Mutex{}, cancels: make(map[uint64]context.CancelFunc)}
}

// start derives the action's context: the stream context bounded by the
// CP's deadline when the request carries one.
func (a *inflightActions) start(
	streamCtx context.Context, req *hostproxyv1.HostActionRequest,
) (context.Context, context.CancelFunc) {
	var actx context.Context
	var acancel context.CancelFunc
	if ms := req.GetDeadlineUnixMs(); ms > 0 {
		actx, acancel = context.WithDeadline(streamCtx, time.UnixMilli(ms))
	} else {
		actx, acancel = context.WithCancel(streamCtx)
	}
	a.mu.Lock()
	a.cancels[req.GetId()] = acancel
	a.mu.Unlock()
	return actx, acancel
}

func (a *inflightActions) cancel(id uint64) {
	a.mu.Lock()
	acancel, ok := a.cancels[id]
	a.mu.Unlock()
	if ok {
		acancel()
	}
}

func (a *inflightActions) done(id uint64) {
	a.mu.Lock()
	delete(a.cancels, id)
	a.mu.Unlock()
}
