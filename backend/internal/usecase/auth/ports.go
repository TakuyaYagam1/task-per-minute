package auth

import (
	"context"
	"time"
)

type Clock interface {
	Now() time.Time
}

type PasswordVerifier interface {
	Verify(password string) bool
}

type RevocationStore interface {
	Revoke(ctx context.Context, jti string, expiresAt time.Time) error
	IsRevoked(ctx context.Context, jti string) (bool, error)
}

type TokenCodec interface {
	Issue(subject string, kind TokenKind, issuedAt, expiresAt time.Time) (string, error)
	Parse(token string) (*Claims, error)
}
