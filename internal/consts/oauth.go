package consts

// OAuth2 protocol vocabulary shared by clawkerd's registration flow,
// the CP's Hydra wiring, and the CLI admin client.
const (
	// OAuth2TokenPath is Hydra's public token endpoint path.
	OAuth2TokenPath = "/oauth2/token"
	// OAuth2IntrospectPath is Hydra's admin token introspection endpoint path.
	OAuth2IntrospectPath = "/admin/oauth2/introspect"
	// GrantTypeClientCredentials is the OAuth2 client_credentials grant type.
	GrantTypeClientCredentials = "client_credentials"
	// ClientAssertionTypeJWTBearer is the private_key_jwt client
	// authentication assertion type (RFC 7523).
	ClientAssertionTypeJWTBearer = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	// HeaderAuthorization is the authorization metadata key. Lowercase is
	// load-bearing: gRPC metadata keys are normalized to lowercase.
	HeaderAuthorization = "authorization"
	// BearerPrefix prefixes a bearer token in an authorization value.
	BearerPrefix = "Bearer "
)
