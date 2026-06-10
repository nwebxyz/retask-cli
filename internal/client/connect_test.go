// internal/client/connect_test.go
package client_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwebxyz/retask-cli/internal/client"
)

func TestBaseURL(t *testing.T) {
	tests := []struct {
		endpoint string
		insecure bool
		want     string
	}{
		{"api.nweb.app:443", false, "https://api.nweb.app"},
		{"api.nweb.app:8080", false, "https://api.nweb.app:8080"},
		{"localhost:8080", true, "http://localhost:8080"},
		{"api.nweb.app", false, "https://api.nweb.app"},
		{"https://api.nweb.app", false, "https://api.nweb.app"},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s/insecure=%v", tc.endpoint, tc.insecure), func(t *testing.T) {
			got := client.BaseURL(tc.endpoint, tc.insecure)
			if got != tc.want {
				t.Errorf("BaseURL(%q, %v) = %q, want %q", tc.endpoint, tc.insecure, got, tc.want)
			}
		})
	}
}

func TestOptionsHTTP(t *testing.T) {
	opts := client.Options("http")
	if len(opts) != 1 {
		t.Errorf("Options(\"http\") returned %d options, want 1 (gRPC-Web)", len(opts))
	}
}

func TestOptionsGRPC(t *testing.T) {
	for _, transport := range []string{"", "grpc", "unknown"} {
		opts := client.Options(transport)
		if len(opts) != 1 {
			t.Errorf("Options(%q) returned %d options, want 1", transport, len(opts))
		}
	}
}

func TestNewInjectsAuthHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := client.New("my-token", false)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got != "Bearer my-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer my-token")
	}
}

func TestNewSkipsAuthHeaderWhenEmpty(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := client.New("", false)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got != "" {
		t.Errorf("Authorization = %q, want empty for empty JWT", got)
	}
}
