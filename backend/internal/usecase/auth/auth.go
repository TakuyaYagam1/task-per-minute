package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	adminSubject           = "admin"
	DefaultClockSkewLeeway = 10 * time.Second
)

type Config struct {
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	ClockSkewLeeway time.Duration
}

type UseCase struct {
	cfg         Config
	clock       Clock
	revocations RevocationStore
	tokens      TokenCodec
	passwords   PasswordVerifier
}

func NewUseCase(
	cfg Config,
	clock Clock,
	revocations RevocationStore,
	tokens TokenCodec,
	passwords PasswordVerifier,
) *UseCase {
	if cfg.ClockSkewLeeway <= 0 {
		cfg.ClockSkewLeeway = DefaultClockSkewLeeway
	}
	return &UseCase{
		cfg:         cfg,
		clock:       clock,
		revocations: revocations,
		tokens:      tokens,
		passwords:   passwords,
	}
}

func (u *UseCase) Login(_ context.Context, password string) (*TokenPair, error) {
	if u.passwords == nil || !u.passwords.Verify(password) {
		return nil, domain.ErrInvalidCredentials
	}
	pair, err := u.issuePair(adminSubject)
	if err != nil {
		return nil, fmt.Errorf("auth login issue pair: %w", err)
	}
	return pair, nil
}

func (u *UseCase) Refresh(ctx context.Context, refreshToken string, accessTokens ...string) (*TokenPair, error) {
	claims, err := u.parse(refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.Kind != TokenKindRefresh {
		return nil, domain.ErrInvalidCredentials
	}

	if err := u.revocations.Revoke(ctx, claims.JTI, u.revocationExpiresAt(claims)); err != nil {
		if errors.Is(err, domain.ErrTokenRevoked) {
			return nil, domain.ErrTokenRevoked
		}
		return nil, fmt.Errorf("auth refresh revoke token: %w", err)
	}
	for _, token := range accessTokens {
		if err := u.revokeAccessToken(ctx, token); err != nil {
			return nil, err
		}
	}

	pair, err := u.issuePair(claims.Subject)
	if err != nil {
		return nil, fmt.Errorf("auth refresh issue pair: %w", err)
	}
	return pair, nil
}

func (u *UseCase) VerifyAccess(ctx context.Context, token string) (*Claims, error) {
	claims, err := u.parse(token)
	if err != nil {
		return nil, err
	}
	if claims.Kind != TokenKindAccess {
		return nil, domain.ErrInvalidCredentials
	}
	revoked, err := u.revocations.IsRevoked(ctx, claims.JTI)
	if err != nil {
		return nil, fmt.Errorf("auth verify access revocation: %w", err)
	}
	if revoked {
		return nil, domain.ErrTokenRevoked
	}
	return claims, nil
}

func (u *UseCase) Logout(ctx context.Context, refreshToken string, accessTokens ...string) error {
	claims, err := u.parse(refreshToken)
	if err != nil {
		return err
	}
	if claims.Kind != TokenKindRefresh {
		return domain.ErrInvalidCredentials
	}
	if err := u.revocations.Revoke(ctx, claims.JTI, u.revocationExpiresAt(claims)); err != nil {
		if errors.Is(err, domain.ErrTokenRevoked) {
			return domain.ErrTokenRevoked
		}
		return fmt.Errorf("auth logout revoke refresh token: %w", err)
	}
	for _, token := range accessTokens {
		if err := u.revokeAccessToken(ctx, token); err != nil {
			return err
		}
	}
	return nil
}

func (u *UseCase) revokeAccessToken(ctx context.Context, token string) error {
	claims, err := u.parse(token)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidCredentials) || errors.Is(err, domain.ErrTokenExpired) {
			return nil
		}
		return err
	}
	if claims.Kind != TokenKindAccess {
		return nil
	}
	if err := u.revocations.Revoke(ctx, claims.JTI, u.revocationExpiresAt(claims)); err != nil {
		if errors.Is(err, domain.ErrTokenRevoked) {
			return nil
		}
		return fmt.Errorf("auth revoke access token: %w", err)
	}
	return nil
}

func (u *UseCase) revocationExpiresAt(claims *Claims) time.Time {
	if claims == nil {
		return time.Time{}
	}
	return claims.ExpiresAt.Add(u.cfg.ClockSkewLeeway)
}

func (u *UseCase) issuePair(subject string) (*TokenPair, error) {
	now := u.clock.Now()
	accessExpiresAt := now.Add(u.cfg.AccessTTL)
	refreshExpiresAt := now.Add(u.cfg.RefreshTTL)

	access, err := u.tokens.Issue(subject, TokenKindAccess, now, accessExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("auth issue access token: %w", err)
	}
	refresh, err := u.tokens.Issue(subject, TokenKindRefresh, now, refreshExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("auth issue refresh token: %w", err)
	}
	return &TokenPair{
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func (u *UseCase) parse(token string) (*Claims, error) {
	if u.tokens == nil {
		return nil, domain.ErrInvalidCredentials
	}
	claims, err := u.tokens.Parse(token)
	if err != nil {
		return nil, err
	}
	if claims == nil || claims.Subject != adminSubject || claims.JTI == "" ||
		claims.IssuedAt.IsZero() || !claims.ExpiresAt.After(claims.IssuedAt) {
		return nil, domain.ErrInvalidCredentials
	}
	return claims, nil
}
