package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type AuthService struct{ repository repository.Repository }

type CreatedToken struct {
	ID     string   `json:"id"`
	Value  string   `json:"value"`
	Scopes []string `json:"scopes"`
}

func NewAuthService(repo repository.Repository) *AuthService { return &AuthService{repository: repo} }

func (s *AuthService) EnsureBootstrap(ctx context.Context, value string) error {
	if value == "" {
		return nil
	}
	encoded := value
	if len(encoded) < 5 || encoded[:4] != "frt_" {
		return types.NewError(types.ErrValidation, "bootstrap token has an invalid format", "supply frt_ followed by a base64url-encoded 256-bit value", nil)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded[4:])
	if err != nil || len(raw) != 32 {
		return types.NewError(types.ErrValidation, "bootstrap token must contain 256 random bits", "generate 32 random bytes and encode them with unpadded base64url", err)
	}
	hash := tokenHash(value)
	if existing, lookupErr := s.repository.GetTokenByHash(ctx, hash); lookupErr == nil && existing.RevokedAt == nil {
		return nil
	}
	return s.repository.CreateToken(ctx, repository.Token{ID: uuid.NewString(), Name: "bootstrap", Hash: hash, Scopes: []string{"ingest", "read", "replay", "admin"}, CreatedAt: time.Now().UTC()})
}

func (s *AuthService) Create(ctx context.Context, name string, scopes []string) (CreatedToken, error) {
	if name == "" || len(scopes) == 0 {
		return CreatedToken{}, types.NewError(types.ErrValidation, "token name and scopes are required", "provide a name and at least one of ingest, read, replay, admin", nil)
	}
	allowed := map[string]bool{"ingest": true, "read": true, "replay": true, "admin": true}
	for _, scope := range scopes {
		if !allowed[scope] {
			return CreatedToken{}, types.NewError(types.ErrValidation, "invalid token scope", "use ingest, read, replay, or admin", nil)
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return CreatedToken{}, types.NewError(types.ErrStorage, "cannot generate secure token", "check system entropy", err)
	}
	value := "frt_" + base64.RawURLEncoding.EncodeToString(raw)
	id := uuid.NewString()
	if err := s.repository.CreateToken(ctx, repository.Token{ID: id, Name: name, Hash: tokenHash(value), Scopes: scopes, CreatedAt: time.Now().UTC()}); err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{ID: id, Value: value, Scopes: scopes}, nil
}

func (s *AuthService) Authorize(ctx context.Context, value, required string) error {
	if value == "" {
		return types.NewError(types.ErrAuthentication, "API token is required", "send Authorization: Bearer <token>", nil)
	}
	token, err := s.repository.GetTokenByHash(ctx, tokenHash(value))
	if err != nil || token.RevokedAt != nil {
		return types.NewError(types.ErrAuthentication, "invalid or revoked API token", "create or supply an active scoped token", err)
	}
	allowed := false
	for _, scope := range token.Scopes {
		if subtle.ConstantTimeCompare([]byte(scope), []byte(required)) == 1 || subtle.ConstantTimeCompare([]byte(scope), []byte("admin")) == 1 {
			allowed = true
		}
	}
	if !allowed {
		return types.NewError(types.ErrAuthentication, "API token lacks required scope", "use a token with "+required+" scope", nil)
	}
	return nil
}

func (s *AuthService) Scopes(ctx context.Context, value string) ([]string, error) {
	if value == "" {
		return nil, types.NewError(types.ErrAuthentication, "API token is required", "enter a token with read access", nil)
	}
	token, err := s.repository.GetTokenByHash(ctx, tokenHash(value))
	if err != nil || token.RevokedAt != nil {
		return nil, types.NewError(types.ErrAuthentication, "invalid or revoked API token", "enter an active token with read access", err)
	}
	return append([]string(nil), token.Scopes...), nil
}

func (s *AuthService) Revoke(ctx context.Context, id string) error {
	return s.repository.RevokeToken(ctx, id)
}
func (s *AuthService) Audit(ctx context.Context, after int64) ([]repository.AuditEvent, error) {
	return s.repository.ListAuditEvents(ctx, after)
}

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
