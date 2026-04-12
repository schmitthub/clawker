package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// DialCPAdmin connects to the CP's gRPC AdminService with TLS + OAuth2.
//
//  1. Load signing key + server cert from dataDir
//  2. Build TLS config trusting the server cert
//  3. Sign a JWT assertion (private_key_jwt) with the CLI's ES256 key
//  4. Exchange assertion for access token via Hydra /oauth2/token
//  5. Dial gRPC with TLS + bearer token in metadata
func DialCPAdmin(ctx context.Context, dataDir string, adminPort, hydraPort int) (adminv1.AdminServiceClient, *grpc.ClientConn, error) {
	signingKey, err := LoadSigningKey(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load signing key: %w", err)
	}

	serverCert, err := ServerTLSCert(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load server cert: %w", err)
	}

	certPool := x509.NewCertPool()
	certPool.AddCert(serverCert)
	tlsCfg := &tls.Config{
		RootCAs:    certPool,
		ServerName: "clawker-cp",
		MinVersion: tls.VersionTLS13,
	}

	hydraTokenURL := fmt.Sprintf("https://127.0.0.1:%d/oauth2/token", hydraPort)
	token, err := fetchAccessToken(ctx, signingKey, hydraTokenURL, tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch access token: %w", err)
	}

	target := fmt.Sprintf("127.0.0.1:%d", adminPort)
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithUnaryInterceptor(bearerInterceptor(token)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("dial cp grpc: %w", err)
	}

	return adminv1.NewAdminServiceClient(conn), conn, nil
}

// fetchAccessToken signs a JWT assertion and exchanges it at Hydra's
// /oauth2/token endpoint for an access token.
func fetchAccessToken(ctx context.Context, signingKey *ecdsa.PrivateKey, tokenURL string, tlsCfg *tls.Config) (string, error) {
	assertion, err := BuildSignedAssertion(AssertionClaims{
		Issuer:           "clawker-cli",
		Subject:          "clawker-cli",
		Audience:         tokenURL,
		JWTID:            uuid.NewString(),
		ExpiresInSeconds: 30,
	}, signingKey)
	if err != nil {
		return "", fmt.Errorf("build assertion: %w", err)
	}

	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
		"scope":                 {"admin"},
	}

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("hydra token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("hydra returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("hydra returned empty access_token")
	}
	return tokenResp.AccessToken, nil
}

// bearerInterceptor adds "authorization: Bearer <token>" to every outgoing RPC.
func bearerInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
