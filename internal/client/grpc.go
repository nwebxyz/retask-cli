// internal/client/grpc.go
package client

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// New creates a gRPC client connection with TLS and JWT auth interceptor.
// Uses OS cert pool — works on macOS, Linux, Windows without bundled certs.
// Pass insecureFlag=true only for local dev against localhost.
func New(endpoint, jwt string, insecureFlag bool) (*grpc.ClientConn, error) {
	var creds grpc.DialOption
	if insecureFlag {
		creds = grpc.WithTransportCredentials(insecure.NewCredentials())
	} else {
		creds = grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, ""))
	}
	return grpc.NewClient(
		endpoint,
		creds,
		grpc.WithUnaryInterceptor(authInterceptor(jwt)),
	)
}

func authInterceptor(jwt string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if jwt != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+jwt)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
