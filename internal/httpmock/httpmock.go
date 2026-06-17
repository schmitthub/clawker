// Package httpmock is an http.RoundTripper test double for stubbing outbound
// HTTP in unit tests, keeping them off the live network with NO test-only seam
// in production code. It mirrors cli/cli's pkg/httpmock pattern: a Registry
// implements http.RoundTripper and matches each request against registered
// stubs. Callers inject Registry.Client() wherever production code accepts an
// *http.Client (e.g. the Factory's HttpClient noun), so the request URL is the
// real one — the transport, not a swapped URL, is the seam.
package httpmock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Registry is an http.RoundTripper test double. Register stubs in the order you
// expect requests; each request is matched against stubs in registration order
// and served by the first match. Every request is recorded (Requests) for
// assertions, and an unmatched request returns an error — so a test that makes
// an unexpected call fails loudly instead of reaching the network.
type Registry struct {
	mu       sync.Mutex
	stubs    []stub
	Requests []*http.Request
}

// Matcher reports whether a request should be served by a stub.
type Matcher func(*http.Request) bool

// Responder produces the response (or error) for a matched request.
type Responder func(*http.Request) (*http.Response, error)

type stub struct {
	matcher   Matcher
	responder Responder
}

// Register adds a stub. The first registered stub whose matcher accepts a
// request serves it.
func (r *Registry) Register(m Matcher, resp Responder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs = append(r.stubs, stub{matcher: m, responder: resp})
}

// RoundTrip implements http.RoundTripper.
func (r *Registry) RoundTrip(req *http.Request) (*http.Response, error) {
	// Honor a cancelled/expired context like the real transport does, so tests
	// can exercise cancellation without a live server.
	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.Requests = append(r.Requests, req)
	var responder Responder
	for _, s := range r.stubs {
		if s.matcher(req) {
			responder = s.responder
			break
		}
	}
	r.mu.Unlock()

	if responder == nil {
		return nil, fmt.Errorf("httpmock: no stub registered for %s %s", req.Method, req.URL)
	}
	return responder(req)
}

// Client returns an *http.Client whose Transport is this registry — pass it
// wherever production code wants an *http.Client.
func (r *Registry) Client() *http.Client {
	return &http.Client{Transport: r}
}

// MatchAny matches every request.
func MatchAny(*http.Request) bool { return true }

// REST matches a request by HTTP method and a substring of its URL path.
// clawker's outbound calls are few and distinct by path, so a substring match
// is sufficient and reads clearly at the call site.
func REST(method, pathContains string) Matcher {
	return func(req *http.Request) bool {
		return req.Method == method && strings.Contains(req.URL.Path, pathContains)
	}
}

// StatusStringResponse responds with status and a raw string body.
func StatusStringResponse(status int, body string) Responder {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}
}

// StringResponse is a 200 with a raw string body.
func StringResponse(body string) Responder {
	return StatusStringResponse(http.StatusOK, body)
}

// StatusJSONResponse responds with status and the JSON encoding of v.
func StatusJSONResponse(status int, v any) Responder {
	return func(*http.Request) (*http.Response, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader(b)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}
}

// JSONResponse is a 200 whose body is the JSON encoding of v.
func JSONResponse(v any) Responder {
	return StatusJSONResponse(http.StatusOK, v)
}
