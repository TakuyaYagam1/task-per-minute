package admin

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

// Aliases to canonical declarations in internal/usecase/contracts.go so
// admin.TokenPair / admin.Claims and usecase.TokenPair / usecase.Claims refer
// to the same types — a prerequisite for *AuthUsecase to satisfy
// usecase.AdminAuth.
type (
	TokenKind = usecase.TokenKind
	Claims    = usecase.Claims
	TokenPair = usecase.TokenPair
)

const (
	TokenKindAccess  = usecase.TokenKindAccess
	TokenKindRefresh = usecase.TokenKindRefresh

	adminSubject = "admin"
)

// AuthConfig is the narrow value type the usecase needs. It is the usecase's
// own contract — wiring (cmd/main, wire) projects the global *config.Config
// onto this struct so the usecase stays decoupled from env-binding shape.
type AuthConfig struct {
	Secret        []byte
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	AdminPassword []byte
}

type AuthUsecase struct {
	cfg         AuthConfig
	clock       clock.Clock
	revocations usecase.RevocationStore
}

func NewAuthUsecase(cfg AuthConfig, clk clock.Clock, rev usecase.RevocationStore) *AuthUsecase {
	return &AuthUsecase{cfg: cfg, clock: clk, revocations: rev}
}

func (u *AuthUsecase) Login(ctx context.Context, password string) (*TokenPair, error) {
	if subtle.ConstantTimeCompare([]byte(password), u.cfg.AdminPassword) != 1 {
		return nil, apperr.ErrInvalidCredentials
	}
	pair, err := u.issuePair(adminSubject)
	if err != nil {
		return nil, fmt.Errorf("AuthUsecase - Login - issuePair: %w", err)
	}
	_ = ctx
	return pair, nil
}

func (u *AuthUsecase) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := u.parse(refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.Kind != TokenKindRefresh {
		return nil, apperr.ErrInvalidCredentials
	}

	revoked, err := u.revocations.IsRevoked(ctx, claims.JTI)
	if err != nil {
		return nil, fmt.Errorf("AuthUsecase - Refresh - RevocationStore.IsRevoked: %w", err)
	}
	if revoked {
		return nil, apperr.ErrTokenRevoked
	}

	if err := u.revocations.Revoke(ctx, claims.JTI, claims.ExpiresAt); err != nil {
		return nil, fmt.Errorf("AuthUsecase - Refresh - RevocationStore.Revoke: %w", err)
	}

	pair, err := u.issuePair(claims.Subject)
	if err != nil {
		return nil, fmt.Errorf("AuthUsecase - Refresh - issuePair: %w", err)
	}
	return pair, nil
}

func (u *AuthUsecase) VerifyAccess(_ context.Context, token string) (*Claims, error) {
	claims, err := u.parse(token)
	if err != nil {
		return nil, err
	}
	if claims.Kind != TokenKindAccess {
		return nil, apperr.ErrInvalidCredentials
	}
	return claims, nil
}

func (u *AuthUsecase) issuePair(sub string) (*TokenPair, error) {
	now := u.clock.Now()
	accessExp := now.Add(u.cfg.AccessTTL)
	refreshExp := now.Add(u.cfg.RefreshTTL)

	access, err := u.sign(sub, TokenKindAccess, now, accessExp)
	if err != nil {
		return nil, fmt.Errorf("AuthUsecase - issuePair - sign access: %w", err)
	}
	refresh, err := u.sign(sub, TokenKindRefresh, now, refreshExp)
	if err != nil {
		return nil, fmt.Errorf("AuthUsecase - issuePair - sign refresh: %w", err)
	}
	return &TokenPair{
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
	}, nil
}

func (u *AuthUsecase) sign(sub string, kind TokenKind, iat, exp time.Time) (string, error) {
	claims := jwt.MapClaims{
		"sub":  sub,
		"kind": string(kind),
		"iat":  iat.Unix(),
		"exp":  exp.Unix(),
		"jti":  uuid.NewString(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(u.cfg.Secret)
	if err != nil {
		return "", fmt.Errorf("AuthUsecase - sign - jwt.SignedString: %w", err)
	}
	return signed, nil
}

func (u *AuthUsecase) parse(tokenStr string) (*Claims, error) {
	parsed, err := jwt.Parse(tokenStr,
		func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method: %s", t.Method.Alg())
			}
			return u.cfg.Secret, nil
		},
		jwt.WithTimeFunc(u.clock.Now),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, apperr.ErrTokenExpired
		}
		return nil, apperr.ErrInvalidCredentials
	}
	if !parsed.Valid {
		return nil, apperr.ErrInvalidCredentials
	}

	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, apperr.ErrInvalidCredentials
	}

	sub, _ := mc["sub"].(string)
	kindStr, _ := mc["kind"].(string)
	jti, _ := mc["jti"].(string)
	iat, _ := mc["iat"].(float64)
	exp, _ := mc["exp"].(float64)

	if jti == "" || sub == "" || kindStr == "" {
		return nil, apperr.ErrInvalidCredentials
	}

	return &Claims{
		JTI:       jti,
		Subject:   sub,
		Kind:      TokenKind(kindStr),
		IssuedAt:  time.Unix(int64(iat), 0).UTC(),
		ExpiresAt: time.Unix(int64(exp), 0).UTC(),
	}, nil
}
