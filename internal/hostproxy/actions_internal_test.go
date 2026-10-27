package hostproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	hostproxyv1 "github.com/schmitthub/clawker/api/hostproxy/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// getFreePort returns an available TCP port on localhost.
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", consts.Localhost+":0")
	require.NoError(t, err, "get free port")
	addr, ok := l.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.NoError(t, l.Close())
	return addr.Port
}

// newTestActions returns Actions with a recording browser stub; no test
// opens a real browser.
func newTestActions(t *testing.T, rulesFile string) (*Actions, *[]string) {
	t.Helper()
	a := NewActions(logger.Nop(), rulesFile)
	opened := new([]string)
	a.browserFunc = func(u string) error { *opened = append(*opened, u); return nil }
	t.Cleanup(func() { a.Close(context.Background()) })
	return a, opened
}

func codeOf(res *hostproxyv1.HostActionResult) codes.Code {
	if e := res.GetError(); e != nil {
		return codes.Code(e.GetCode())
	}
	return codes.OK
}

func openURL(u string) *hostproxyv1.HostActionRequest {
	return &hostproxyv1.HostActionRequest{Id: 1, Action: &hostproxyv1.HostActionRequest_OpenUrl{
		OpenUrl: &agentv1.OpenURLRequest{Url: u},
	}}
}

func registerCallback(authURL string, ttl int32) *hostproxyv1.HostActionRequest {
	return &hostproxyv1.HostActionRequest{Id: 2, Action: &hostproxyv1.HostActionRequest_RegisterCallback{
		RegisterCallback: &agentv1.RegisterCallbackRequest{AuthUrl: authURL, TtlSeconds: ttl},
	}}
}

func getCallbackData(id string) *hostproxyv1.HostActionRequest {
	return &hostproxyv1.HostActionRequest{Id: 3, Action: &hostproxyv1.HostActionRequest_GetCallbackData{
		GetCallbackData: &agentv1.GetCallbackDataRequest{SessionId: id},
	}}
}

func deleteCallback(id string) *hostproxyv1.HostActionRequest {
	return &hostproxyv1.HostActionRequest{Id: 4, Action: &hostproxyv1.HostActionRequest_DeleteCallback{
		DeleteCallback: &agentv1.DeleteCallbackRequest{SessionId: id},
	}}
}

func gitCredential(req *agentv1.GitCredentialRequest) *hostproxyv1.HostActionRequest {
	return &hostproxyv1.HostActionRequest{
		Id:     5,
		Action: &hostproxyv1.HostActionRequest_GitCredential{GitCredential: req},
	}
}

func TestActions_EchoesID(t *testing.T) {
	a, _ := newTestActions(t, "")
	req := openURL("https://example.test/")
	req.Id = 42
	assert.Equal(t, uint64(42), a.Handle(context.Background(), req).GetId())
}

func TestActions_UnknownAction(t *testing.T) {
	a, _ := newTestActions(t, "")
	res := a.Handle(context.Background(), &hostproxyv1.HostActionRequest{Id: 1, Action: nil})
	assert.Equal(t, codes.InvalidArgument, codeOf(res))
}

func TestActions_OpenURL_Rejects(t *testing.T) {
	for name, u := range map[string]string{
		"empty url":           "",
		"file scheme":         "file:///etc/passwd",
		"javascript scheme":   "javascript:alert(1)",
		"data scheme":         "data:text/html,<script>alert(1)</script>",
		"relative url":        "/etc/passwd",
		"unparseable control": "https://exa\x7fmple.test/",
	} {
		t.Run(name, func(t *testing.T) {
			a, opened := newTestActions(t, "")
			res := a.Handle(context.Background(), openURL(u))
			assert.Equal(t, codes.InvalidArgument, codeOf(res))
			assert.Empty(t, *opened, "browser must not open")
		})
	}
}

func TestActions_OpenURL_EgressBlocking(t *testing.T) {
	rulesFile := filepath.Join(t.TempDir(), "egress-rules.yaml")
	rulesYAML := `rules:
  - action: allow
    dst: github.test
    port: 443
    proto: tls
  - action: allow
    dst: docs.example.test
    port: 80
    proto: http
`
	require.NoError(t, os.WriteFile(rulesFile, []byte(rulesYAML), 0o644))

	cases := []struct {
		name string
		url  string
		want codes.Code
	}{
		{"allowed domain", "https://github.test/schmitthub/clawker", codes.OK},
		{"blocked domain", "https://evil.test/exfil?data=stolen", codes.PermissionDenied},
		{"wrong proto for domain", "http://github.test/foo", codes.PermissionDenied},
		{"http allowed domain", "http://docs.example.test/guide", codes.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, opened := newTestActions(t, rulesFile)
			res := a.Handle(context.Background(), openURL(tc.url))
			require.Equal(t, tc.want, codeOf(res), res.GetError().GetMessage())
			if tc.want == codes.OK {
				assert.NotNil(t, res.GetOpenUrl())
				assert.Equal(t, []string{tc.url}, *opened)
			} else {
				assert.Empty(t, *opened)
			}
		})
	}
}

func TestActions_OpenURL_NoRulesFile_SkipsCheck(t *testing.T) {
	a, opened := newTestActions(t, "")
	res := a.Handle(context.Background(), openURL("https://any-domain.test/path"))
	require.Equal(t, codes.OK, codeOf(res))
	assert.Len(t, *opened, 1)
}

// TestActions_OpenURL_BrokenRulesFails: firewall enabled (path set) but the
// rules file is missing or corrupt — a broken trust boundary, not a policy
// deny. It fails closed with Internal, distinct from PermissionDenied.
func TestActions_OpenURL_BrokenRulesFails(t *testing.T) {
	corrupt := filepath.Join(t.TempDir(), "corrupt.yaml")
	require.NoError(
		t,
		os.WriteFile(corrupt, []byte("rules:\n  - dst: github.test\n    proto: https\n    port: \"443\"\n"+
			"    path_rules:\n      - action: deny\n        path: \"~/[bad(\"\n"), 0o600),
	)
	for name, path := range map[string]string{
		"missing": "/nonexistent/egress-rules.yaml",
		"corrupt": corrupt,
	} {
		t.Run(name, func(t *testing.T) {
			a, opened := newTestActions(t, path)
			res := a.Handle(context.Background(), openURL("https://github.test/"))
			assert.Equal(t, codes.Internal, codeOf(res))
			assert.Equal(t, errEgressRulesUnavailable, res.GetError().GetMessage())
			assert.Empty(t, *opened)
		})
	}
}

func TestActions_OpenURL_BrowserFailure(t *testing.T) {
	a, _ := newTestActions(t, "")
	a.browserFunc = func(string) error { return errors.New("no browser") }
	assert.Equal(t, codes.Internal, codeOf(a.Handle(context.Background(), openURL("https://example.test/"))))
}

// --- Callback flow ---

// authURLFor builds an OAuth authorization URL whose redirect_uri points
// at a localhost callback on port.
func authURLFor(host string, port int) string {
	redirect := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/callback"
	return "https://auth.example.com/authorize?client_id=x&redirect_uri=" + url.QueryEscape(redirect)
}

// TestActions_RegisterCallback pins the one-shot OAuth contract: the host
// proxy takes the callback port and path from the auth URL's localhost
// redirect, binds it, and opens the auth URL itself.
func TestActions_RegisterCallback(t *testing.T) {
	a, opened := newTestActions(t, "")
	port := getFreePort(t)
	authURL := authURLFor("localhost", port)

	res := a.Handle(context.Background(), registerCallback(authURL, 120))
	require.Equal(t, codes.OK, codeOf(res), res.GetError().GetMessage())
	reg := res.GetRegisterCallback()
	require.NotEmpty(t, reg.GetSessionId())
	assert.Equal(t, int32(port), reg.GetPort())
	got, _ := a.callbackChannel.GetPath(reg.GetSessionId())
	assert.Equal(t, "/callback", got)
	assert.Equal(t, []string{authURL}, *opened)
}

// TestActions_RegisterCallback_Rejects: everything that is not a loopback
// http redirect on an unprivileged port is refused before any listener
// binds or the browser opens.
func TestActions_RegisterCallback_Rejects(t *testing.T) {
	port := getFreePort(t)
	for name, authURL := range map[string]string{
		"no redirect":     "https://auth.example.com/authorize",
		"remote redirect": authURLFor("evil.example.com", port),
		"privileged port": authURLFor("localhost", 80),
		"https redirect":  "https://a.example.com/?redirect_uri=https%3A%2F%2Flocalhost%3A40000%2Fcb",
		"no port":         "https://a.example.com/?redirect_uri=http%3A%2F%2Flocalhost%2Fcb",
		"userinfo":        "https://a.example.com/?redirect_uri=http%3A%2F%2Fu%3Ap%40localhost%3A40000%2Fcb",
	} {
		t.Run(name, func(t *testing.T) {
			a, opened := newTestActions(t, "")
			res := a.Handle(context.Background(), registerCallback(authURL, 0))
			assert.Equal(t, codes.InvalidArgument, codeOf(res))
			assert.Empty(t, *opened, "browser must not open")
			assert.Zero(t, a.sessionStore.Count(), "no session may remain")
		})
	}
}

// TestActions_RegisterCallback_EgressDenied: the auth URL is egress-checked
// like OpenURL; a denied host opens nothing and binds nothing.
func TestActions_RegisterCallback_EgressDenied(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "egress-rules.yaml")
	require.NoError(t, os.WriteFile(rules, []byte("rules:\n  - dst: allowed.example.com\n    proto: https\n"), 0o600))
	a, opened := newTestActions(t, rules)
	res := a.Handle(context.Background(), registerCallback(authURLFor("localhost", getFreePort(t)), 0))
	assert.Equal(t, codes.PermissionDenied, codeOf(res))
	assert.Empty(t, *opened)
	assert.Zero(t, a.sessionStore.Count())
}

// TestActions_RegisterCallback_TTLCapped: the session never outlives the
// default callback TTL, whatever the caller asks for.
func TestActions_RegisterCallback_TTLCapped(t *testing.T) {
	a, _ := newTestActions(t, "")
	res := a.Handle(context.Background(), registerCallback(authURLFor(consts.Localhost, getFreePort(t)), 86400))
	require.Equal(t, codes.OK, codeOf(res))
	expires := time.Unix(res.GetRegisterCallback().GetExpiresAtUnix(), 0)
	assert.False(t, expires.After(time.Now().Add(DefaultCallbackTTL+time.Second)),
		"expiry %v exceeds the %v cap", expires, DefaultCallbackTTL)
}

// TestActions_RegisterCallback_BrowserFailure: when the browser cannot
// open, the session and its listener are removed.
func TestActions_RegisterCallback_BrowserFailure(t *testing.T) {
	a, _ := newTestActions(t, "")
	a.browserFunc = func(string) error { return errors.New("no browser") }
	port := getFreePort(t)
	res := a.Handle(context.Background(), registerCallback(authURLFor("localhost", port), 0))
	assert.Equal(t, codes.Internal, codeOf(res))
	assert.Zero(t, a.sessionStore.Count())
	assertPortClosed(t, consts.Localhost, port)
}

func TestActions_GetCallbackData(t *testing.T) {
	a, _ := newTestActions(t, "")
	session, err := a.callbackChannel.Register(8080, "/callback", 5*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	res := a.Handle(ctx, getCallbackData(session.ID))
	require.Equal(t, codes.OK, codeOf(res))
	assert.False(t, res.GetGetCallbackData().GetReceived(), "no callback yet")

	assert.Equal(t, codes.NotFound, codeOf(a.Handle(ctx, getCallbackData("nonexistent"))))
	assert.Equal(t, codes.InvalidArgument, codeOf(a.Handle(ctx, getCallbackData(""))))

	captureReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "/callback?code=ABC&state=XYZ", nil)
	require.NoError(t, err)
	require.NoError(t, a.callbackChannel.Capture(session.ID, captureReq))

	res = a.Handle(ctx, getCallbackData(session.ID))
	require.Equal(t, codes.OK, codeOf(res))
	data := res.GetGetCallbackData()
	require.True(t, data.GetReceived())
	assert.Equal(t, "code=ABC&state=XYZ", data.GetCallback().GetQuery())
	assert.Equal(t, http.MethodGet, data.GetCallback().GetMethod())
	assert.NotZero(t, data.GetCallback().GetReceivedAtUnix())
}

func TestActions_DeleteCallback(t *testing.T) {
	a, _ := newTestActions(t, "")
	session, err := a.callbackChannel.Register(8080, "/callback", 5*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	require.Equal(t, codes.OK, codeOf(a.Handle(ctx, deleteCallback(session.ID))))
	assert.Nil(t, a.sessionStore.Get(session.ID), "session must be gone")
	assert.Equal(t, codes.OK, codeOf(a.Handle(ctx, deleteCallback("nonexistent"))), "delete is idempotent")
	assert.Equal(t, codes.InvalidArgument, codeOf(a.Handle(ctx, deleteCallback(""))))
}

func TestActions_CloseCleansUpSessions(t *testing.T) {
	a := NewActions(logger.Nop(), "")
	_, err := a.callbackChannel.Register(8080, "/callback", 5*time.Minute)
	require.NoError(t, err)
	_, err = a.callbackChannel.Register(8081, "/callback", 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 2, a.sessionStore.Count())
	a.Close(context.Background())
	a.Close(context.Background()) // idempotent
}

func assertPortClosed(t *testing.T, host string, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 100*time.Millisecond)
	if err == nil {
		require.NoError(t, conn.Close())
		t.Errorf("listener on %s:%d must be closed", host, port)
	}
}

func ipv6Loopback() bool {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func TestActions_CallbackListenerStartStop(t *testing.T) {
	a, _ := newTestActions(t, "")
	port := getFreePort(t)
	ctx := context.Background()

	res := a.Handle(ctx, registerCallback(authURLFor("localhost", port), 0))
	require.Equal(t, codes.OK, codeOf(res), res.GetError().GetMessage())

	conn, err := net.DialTimeout("tcp", fmt.Sprintf(consts.Localhost+":%d", port), time.Second)
	require.NoError(t, err, "callback listener must listen on IPv4 loopback")
	require.NoError(t, conn.Close())
	if ipv6Loopback() {
		conn6, err6 := net.DialTimeout("tcp", fmt.Sprintf("[::1]:%d", port), time.Second)
		require.NoError(t, err6, "callback listener must listen on IPv6 loopback")
		require.NoError(t, conn6.Close())
	}

	require.Equal(t, codes.OK, codeOf(a.Handle(ctx, deleteCallback(res.GetRegisterCallback().GetSessionId()))))
	assertPortClosed(t, consts.Localhost, port)
	if ipv6Loopback() {
		assertPortClosed(t, "::1", port)
	}
}

func TestActions_CallbackCapture(t *testing.T) {
	for _, host := range []string{consts.Localhost, "::1"} {
		t.Run(host, func(t *testing.T) {
			if host == "::1" && !ipv6Loopback() {
				t.Skip("IPv6 loopback not available")
			}
			a, _ := newTestActions(t, "")
			port := getFreePort(t)
			ctx := context.Background()
			res := a.Handle(ctx, registerCallback(authURLFor("localhost", port), 0))
			require.Equal(t, codes.OK, codeOf(res), res.GetError().GetMessage())
			sessionID := res.GetRegisterCallback().GetSessionId()

			callbackURL := "http://" + net.JoinHostPort(
				host,
				strconv.Itoa(port),
			) + "/callback?code=AUTH_CODE&state=RANDOM_STATE"
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, callbackURL, nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			data := a.Handle(ctx, getCallbackData(sessionID)).GetGetCallbackData()
			require.True(t, data.GetReceived())
			assert.Contains(t, data.GetCallback().GetQuery(), "code=AUTH_CODE")
			assert.Contains(t, data.GetCallback().GetQuery(), "state=RANDOM_STATE")

			// One-shot: the listener closes after the first capture, so the
			// host port is not held open for the rest of the session.
			require.Eventually(t, func() bool {
				conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf(consts.Localhost+":%d", port), 100*time.Millisecond)
				if dialErr != nil {
					return true
				}
				_ = conn.Close()
				return false
			}, 2*time.Second, 20*time.Millisecond, "callback listener still open after capture")
			assert.True(t, a.Handle(ctx, getCallbackData(sessionID)).GetGetCallbackData().GetReceived(),
				"captured data must survive the listener close")
		})
	}
}

// --- Git credential ---

// gitReq builds a git credential request with every field set.
func gitReq(
	action agentv1.GitCredentialAction,
	protocol, host, path, username, password string,
) *agentv1.GitCredentialRequest {
	return &agentv1.GitCredentialRequest{
		Action: action, Protocol: protocol, Host: host, Path: path, Username: username, Password: password,
	}
}

func TestActions_GitCredential_Rejects(t *testing.T) {
	get := agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET
	store := agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_STORE
	none := agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_UNSPECIFIED
	cases := map[string]*agentv1.GitCredentialRequest{
		"missing action":            gitReq(none, "https", "github.com", "", "", ""),
		"unknown action enum value": gitReq(agentv1.GitCredentialAction(99), "https", "github.com", "", "", ""),
		"missing protocol":          gitReq(get, "", "github.com", "", "", ""),
		"missing host":              gitReq(get, "https", "", "", "", ""),
		"newline in host":           gitReq(get, "https", "evil.com\nusername=victim\npassword=stolen", "", "", ""),
		"carriage return in host":   gitReq(get, "https", "evil.com\r\ninjected=true", "", "", ""),
		"null byte in protocol":     gitReq(get, "https\x00injected", "github.com", "", "", ""),
		"newline in username":       gitReq(store, "https", "github.com", "", "user\npassword=pwned", ""),
		"newline in password":       gitReq(store, "https", "github.com", "", "u", "p\nhost=evil.com"),
		"newline in path":           gitReq(get, "https", "github.com", "repo\nusername=hacker", "", ""),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			a, _ := newTestActions(t, "")
			res := a.Handle(context.Background(), gitCredential(req))
			assert.Equal(t, codes.InvalidArgument, codeOf(res))
		})
	}
}

// TestActions_GitCredential_HelperFailure: the host git credential command
// ran and failed (no helper, prompts disabled). The CP sees a failed
// action (FailedPrecondition), not a credential and not a host fault.
func TestActions_GitCredential_HelperFailure(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_ASKPASS", "")
	t.Setenv("SSH_ASKPASS", "")

	a, _ := newTestActions(t, "")
	res := a.Handle(context.Background(), gitCredential(
		gitReq(agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET, "https", "clawker-test.invalid", "", "", ""),
	))
	assert.Equal(t, codes.FailedPrecondition, codeOf(res), res.GetError().GetMessage())
}
