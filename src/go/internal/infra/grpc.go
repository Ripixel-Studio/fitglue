package infra

import (
	"context"
	"crypto/tls"
	"strings"

	"google.golang.org/api/idtoken"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/oauth"
)

// GRPCDial creates a gRPC client connection to the given target.
//
//   - https://host (Cloud Run): TLS on 443 plus an OIDC identity token for the
//     target audience, which Cloud Run IAM requires.
//   - http://host:port (the box, docker-compose): plaintext h2c, no auth. The
//     services only ever bind 127.0.0.1 there and trust the loopback.
//   - host:port (local development): plaintext, as before.
func GRPCDial(target string) (*grpc.ClientConn, error) {
	if strings.HasPrefix(target, "https://") {
		// Cloud Run URL: strip scheme, use TLS on port 443
		host := strings.TrimPrefix(target, "https://")

		// Obtain an OIDC identity token for the target audience.
		// On Cloud Run, this uses the service account's metadata server automatically.
		tokenSource, err := idtoken.NewTokenSource(context.Background(), target)
		if err != nil {
			return nil, err
		}

		return grpc.NewClient(host+":443",
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
			grpc.WithPerRPCCredentials(&oauth.TokenSource{TokenSource: tokenSource}),
		)
	}
	// Plaintext: strip an http:// scheme so grpc gets a bare host:port rather
	// than an address it would try to resolve literally.
	target = strings.TrimPrefix(target, "http://")
	return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
}
