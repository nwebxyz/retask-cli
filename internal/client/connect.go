// internal/client/connect.go
package client

import (
	"crypto/tls"
	"net/http"
	"strings"

	"connectrpc.com/connect"
)

type authTransport struct {
	jwt  string
	base http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.jwt != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+t.jwt)
	}
	return t.base.RoundTrip(req)
}

// New returns an *http.Client that injects Authorization: Bearer <jwt> on every request.
// Empty jwt skips the header (proxy-injected auth). insecure=true disables TLS verification.
func New(jwt string, insecure bool) *http.Client {
	var base http.RoundTripper = http.DefaultTransport
	if insecure {
		base = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	return &http.Client{Transport: &authTransport{jwt: jwt, base: base}}
}

// BaseURL converts a gRPC-style endpoint to an HTTPS base URL.
// Port 443 is dropped (implied by https://). Non-standard ports are preserved.
// insecure=true produces an http:// scheme.
func BaseURL(endpoint string, insecure bool) string {
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	scheme := "https"
	if insecure {
		scheme = "http"
	}
	if !insecure && strings.HasSuffix(endpoint, ":443") {
		endpoint = strings.TrimSuffix(endpoint, ":443")
	}
	return scheme + "://" + endpoint
}

// Options returns connect.ClientOptions for the given transport string.
// "http" → gRPC-Web over HTTP/1.1 (proxy-friendly). Anything else (including "") → gRPC over HTTP/2.
func Options(transport string) []connect.ClientOption {
	if transport == "http" {
		return []connect.ClientOption{connect.WithGRPCWeb()}
	}
	return []connect.ClientOption{connect.WithGRPC()}
}
