package otel

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	ctest "github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cannedHandler struct {
	msg *dns.Msg
	err error
}

func (c cannedHandler) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	if c.err != nil {
		return dns.RcodeServerFailure, c.err
	}
	resp := c.msg.Copy()
	resp.SetReply(r)
	resp.Answer = c.msg.Answer
	w.WriteMsg(resp)
	return dns.RcodeSuccess, nil
}

func (c cannedHandler) Name() string { return "test" }

var _ plugin.Handler = cannedHandler{}

type recordingEmitter struct {
	events []QueryEvent
	err    error
}

func (r *recordingEmitter) Emit(_ context.Context, event QueryEvent) error {
	r.events = append(r.events, event)
	return r.err
}

func TestServeDNS_EmitsEventAndForwardsResponse(t *testing.T) {
	resp := new(dns.Msg)
	resp.SetReply(&dns.Msg{})
	resp.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "github.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("140.82.121.4"),
		},
	}

	emitter := &recordingEmitter{}
	h := Handler{
		Next:    cannedHandler{msg: resp},
		Zone:    "github.com.",
		Emitter: emitter,
	}

	req := new(dns.Msg)
	req.SetQuestion("github.com.", dns.TypeA)
	w := dnstest.NewRecorder(&ctest.ResponseWriter{})

	rcode, err := h.ServeDNS(context.Background(), w, req)
	require.NoError(t, err)
	assert.Equal(t, dns.RcodeSuccess, rcode)
	require.NotNil(t, w.Msg)
	require.Len(t, emitter.events, 1)

	event := emitter.events[0]
	assert.Equal(t, "github.com", event.Zone)
	assert.Equal(t, "github.com", event.QueryName)
	assert.Equal(t, "A", event.QueryType)
	assert.Equal(t, "NOERROR", event.RCode)
	assert.Len(t, event.Answers, 1)
	assert.Equal(t, 1, event.AnswerCount)
	assert.NoError(t, event.Err)
	assert.NotZero(t, event.Timestamp)
	assert.NotZero(t, event.Duration)
}

func TestServeDNS_EmitsEventOnResolverError(t *testing.T) {
	emitter := &recordingEmitter{}
	h := Handler{
		Next:    cannedHandler{err: errors.New("boom")},
		Zone:    ".",
		Emitter: emitter,
	}

	req := new(dns.Msg)
	req.SetQuestion("blocked.example.", dns.TypeA)
	w := dnstest.NewRecorder(&ctest.ResponseWriter{})

	rcode, err := h.ServeDNS(context.Background(), w, req)
	require.Error(t, err)
	assert.Equal(t, dns.RcodeServerFailure, rcode)
	require.Len(t, emitter.events, 1)
	assert.Equal(t, "SERVFAIL", emitter.events[0].RCode)
	assert.EqualError(t, emitter.events[0].Err, "boom")
	assert.Equal(t, "blocked.example", emitter.events[0].QueryName)
	assert.Equal(t, 0, emitter.events[0].AnswerCount)
}

func TestRemoteIP(t *testing.T) {
	addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.42"), Port: 5353}
	assert.Equal(t, "10.0.0.42", remoteIP(addr))
}
