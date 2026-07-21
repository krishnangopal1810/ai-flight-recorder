package access

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func RequireHTTPTransport(cfg config.Config, request *http.Request) error {
	if cfg.Server.TLSCert != "" && request.TLS != nil && request.TLS.Version >= tls.VersionTLS12 {
		return nil
	}
	if cfg.Auth.BehindTLSProxy && cfg.Auth.TrustsProxy(request.RemoteAddr) && forwardedHTTPS(request.Header.Values("X-Forwarded-Proto")) {
		return nil
	}
	if cfg.Server.TLSCert == "" && !cfg.Auth.BehindTLSProxy {
		return nil
	}
	return types.NewError(types.ErrAuthentication, "trusted TLS transport is required", "connect with HTTPS through the configured native identity or trusted reverse proxy", nil)
}

func UnaryServerInterceptor(cfg config.Config, auth *service.AuthService) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := requireGRPCTransport(ctx, cfg); err != nil {
			return nil, grpcError(err)
		}
		if !cfg.Auth.LocalBypass {
			incoming, _ := metadata.FromIncomingContext(ctx)
			if err := Authorize(ctx, auth, incoming.Get("authorization"), "ingest"); err != nil {
				return nil, grpcError(err)
			}
		}
		return handler(ctx, request)
	}
}

func Authorize(ctx context.Context, auth *service.AuthService, authorizationValues []string, scope string) error {
	value, err := BearerToken(authorizationValues)
	if err != nil {
		return err
	}
	return auth.Authorize(ctx, value, scope)
}

func BearerToken(values []string) (string, error) {
	if len(values) != 1 {
		return "", types.NewError(types.ErrAuthentication, "exactly one bearer token is required", "send one Authorization: Bearer <token> header", nil)
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", types.NewError(types.ErrAuthentication, "API token uses an invalid authorization scheme", "send Authorization: Bearer <token>", nil)
	}
	return parts[1], nil
}

func requireGRPCTransport(ctx context.Context, cfg config.Config) error {
	connection, ok := peer.FromContext(ctx)
	if cfg.Server.TLSCert != "" && ok {
		if tlsInfo, tlsOK := connection.AuthInfo.(credentials.TLSInfo); tlsOK && tlsInfo.State.Version >= tls.VersionTLS12 {
			return nil
		}
	}
	if cfg.Auth.BehindTLSProxy && ok && cfg.Auth.TrustsProxy(connection.Addr.String()) {
		return nil
	}
	if cfg.Server.TLSCert == "" && !cfg.Auth.BehindTLSProxy {
		return nil
	}
	return types.NewError(types.ErrAuthentication, "trusted TLS transport is required", "connect with TLS through the configured native identity or trusted reverse proxy", nil)
}

func forwardedHTTPS(values []string) bool {
	return len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), "https")
}

func grpcError(err error) error {
	if typed, ok := err.(*types.FlightError); ok {
		return status.Error(codes.Unauthenticated, string(typed.Code)+": "+typed.Message)
	}
	return status.Error(codes.Internal, "internal: access check failed")
}
