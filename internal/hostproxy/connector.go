package hostproxy

import (
	"context"
	"fmt"
	"sync"
	"time"

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

// Connector holds the host proxy's HostProxyService stream to the CP. The
// CP sends authorized host actions down the stream; the connector runs
// each one on Actions and sends back the result. When the stream ends (CP
// restart, network fault, a newer host proxy replaced it), the connector
// opens a new one with backoff.
type Connector struct {
	client  hostproxyv1.HostProxyServiceClient
	actions *Actions
	log     *logger.Logger

	minBackoff time.Duration
	maxBackoff time.Duration
}

// NewConnector returns a Connector that serves actions over client.
func NewConnector(client hostproxyv1.HostProxyServiceClient, actions *Actions, log *logger.Logger) *Connector {
	return &Connector{
		client:     client,
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

// serve opens one stream and serves it until it ends. The bool reports
// whether the CP accepted the stream.
func (c *Connector) serve(ctx context.Context) (bool, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := c.client.Connect(streamCtx)
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

	var (
		sendMu sync.Mutex // gRPC allows one Send at a time
		wg     sync.WaitGroup
	)
	for {
		req, recvErr := stream.Recv()
		if recvErr != nil {
			// Cancel first: a git helper in flight is killed, then wait.
			cancel()
			wg.Wait()
			return true, fmt.Errorf("receive from control plane: %w", recvErr)
		}
		wg.Go(func() {
			res := c.actions.Handle(streamCtx, req)
			sendMu.Lock()
			sendErr := stream.Send(res)
			sendMu.Unlock()
			if sendErr != nil {
				c.log.Debug().Err(sendErr).Uint64("id", req.GetId()).Msg("send host action result")
			}
		})
	}
}
