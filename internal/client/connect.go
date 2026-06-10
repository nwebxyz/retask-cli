// internal/client/connect.go
package client

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
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

type debugTransport struct {
	base http.RoundTripper
}

func contentTypeToProtocol(ct string) string {
	switch {
	case strings.HasPrefix(ct, "application/grpc-web"):
		return "gRPC-Web"
	case strings.HasPrefix(ct, "application/grpc"):
		return "gRPC"
	case strings.HasPrefix(ct, "application/connect"):
		return "Connect"
	default:
		return "HTTP"
	}
}

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	protocol := contentTypeToProtocol(req.Header.Get("Content-Type"))
	fmt.Fprintf(os.Stderr, "[retask] > %s %s [%s]\n", req.Method, req.URL, protocol)
	if req.Header.Get("Authorization") != "" {
		fmt.Fprintf(os.Stderr, "[retask]   Authorization: Bearer [redacted]\n")
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[retask] < ERROR: %v\n", err)
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "[retask] < %s\n", resp.Status)
	return resp, nil
}

// New returns an *http.Client that injects Authorization: Bearer <jwt> on every request.
// Empty jwt skips the header (proxy-injected auth). insecure=true disables TLS verification.
// verbose=true wraps the transport with a debugTransport that logs requests/responses to stderr.
func New(jwt string, insecure bool, verbose bool) *http.Client {
	var base http.RoundTripper = http.DefaultTransport
	if insecure {
		base = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	var inner http.RoundTripper = base
	if verbose {
		inner = &debugTransport{base: base}
	}
	return &http.Client{Transport: &authTransport{jwt: jwt, base: inner}}
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
