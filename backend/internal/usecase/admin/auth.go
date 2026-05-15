package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

type (
	TokenKind = usecase.TokenKind
	Claims    = usecase.Claims
	TokenPair = usecase.TokenPair
)

const (
	TokenKindAccess    = usecase.TokenKindAccess
	TokenKindRefresh   = usecase.TokenKindRefresh
	adminSubject       = "admin"
	jwtClockSkewLeeway = 10 * time.Second
	jwtIssuer          = "task-per-minute-backend"
	jwtAudience        = "task-per-minute-admin"
)

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

func (u *AuthUsecase) Login(_ context.Context, password string) (*TokenPair, error) {
	if !verifyAdminPassword(u.cfg.AdminPassword, password) {
		return nil, apperr.ErrInvalidCredentials
	}
	pair, err := u.issuePair(adminSubject)
	if err != nil {
		return nil, fmt.Errorf("AuthUsecase - Login - issuePair: %w", err)
	}
	return pair, nil
}

func verifyAdminPassword(stored []byte, supplied string) bool {
	if isBcryptHash(stored) {
		return bcrypt.CompareHashAndPassword(stored, []byte(supplied)) == nil
	}
	storedSum := sha256.Sum256(stored)
	suppliedSum := sha256.Sum256([]byte(supplied))
	return subtle.ConstantTimeCompare(storedSum[:], suppliedSum[:]) == 1
}

func isBcryptHash(stored []byte) bool {
	return bytes.HasPrefix(stored, []byte("$2a$")) ||
		bytes.HasPrefix(stored, []byte("$2b$")) ||
		bytes.HasPrefix(stored, []byte("$2y$"))
}

func (u *AuthUsecase) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := u.parse(refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.Kind != TokenKindRefresh {
		return nil, apperr.ErrInvalidCredentials
	}

	if err := u.revocations.Revoke(ctx, claims.JTI, revocationExpiresAt(claims)); err != nil {
		if errors.Is(err, apperr.ErrTokenRevoked) {
			return nil, apperr.ErrTokenRevoked
		}
		return nil, fmt.Errorf("AuthUsecase - Refresh - RevocationStore.Revoke: %w", err)
	}

	pair, err := u.issuePair(claims.Subject)
	if err != nil {
		return nil, fmt.Errorf("AuthUsecase - Refresh - issuePair: %w", err)
	}
	return pair, nil
}

func (u *AuthUsecase) VerifyAccess(ctx context.Context, token string) (*Claims, error) {
	claims, err := u.parse(token)
	if err != nil {
		return nil, err
	}
	if claims.Kind != TokenKindAccess {
		return nil, apperr.ErrInvalidCredentials
	}
	if err := u.ensureNotRevoked(ctx, claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (u *AuthUsecase) Logout(ctx context.Context, refreshToken string, accessTokens ...string) error {
	claims, err := u.parse(refreshToken)
	if err != nil {
		return err
	}
	if claims.Kind != TokenKindRefresh {
		return apperr.ErrInvalidCredentials
	}
	if err := u.revocations.Revoke(ctx, claims.JTI, revocationExpiresAt(claims)); err != nil {
		if errors.Is(err, apperr.ErrTokenRevoked) {
			return apperr.ErrTokenRevoked
		}
		return fmt.Errorf("AuthUsecase - Logout - RevocationStore.Revoke: %w", err)
	}
	for _, token := range accessTokens {
		if err := u.revokeAccessToken(ctx, token); err != nil {
			return err
		}
	}
	return nil
}

func (u *AuthUsecase) ensureNotRevoked(ctx context.Context, claims *Claims) error {
	revoked, err := u.revocations.IsRevoked(ctx, claims.JTI)
	if err != nil {
		return fmt.Errorf("AuthUsecase - VerifyAccess - RevocationStore.IsRevoked: %w", err)
	}
	if revoked {
		return apperr.ErrTokenRevoked
	}
	return nil
}

func (u *AuthUsecase) revokeAccessToken(ctx context.Context, token string) error {
	claims, err := u.parse(token)
	if err != nil {
		if errors.Is(err, apperr.ErrInvalidCredentials) || errors.Is(err, apperr.ErrTokenExpired) {
			return nil
		}
		return err
	}
	if claims.Kind != TokenKindAccess {
		return nil
	}
	if err := u.revocations.Revoke(ctx, claims.JTI, revocationExpiresAt(claims)); err != nil {
		if errors.Is(err, apperr.ErrTokenRevoked) {
			return nil
		}
		return fmt.Errorf("AuthUsecase - Logout - RevocationStore.Revoke access: %w", err)
	}
	return nil
}

func revocationExpiresAt(claims *Claims) time.Time {
	if claims == nil {
		return time.Time{}
	}
	return claims.ExpiresAt.Add(jwtClockSkewLeeway)
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
		"iss":  jwtIssuer,
		"aud":  jwtAudience,
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
		u.jwtKeyfunc,
		jwt.WithTimeFunc(u.clock.Now),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithAudience(jwtAudience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(jwtClockSkewLeeway),
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
	return claimsFromMap(mc)
}

func (u *AuthUsecase) jwtKeyfunc(t *jwt.Token) (any, error) {
	if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
		return nil, fmt.Errorf("unexpected signing method: %s", t.Method.Alg())
	}
	return u.cfg.Secret, nil
}

func claimsFromMap(mc jwt.MapClaims) (*Claims, error) {
	sub, err := adminSubjectFromClaims(mc)
	if err != nil {
		return nil, apperr.ErrInvalidCredentials
	}
	kind, err := tokenKindFromClaims(mc)
	if err != nil {
		return nil, apperr.ErrInvalidCredentials
	}
	jti, err := requiredStringClaim(mc, "jti")
	if err != nil {
		return nil, apperr.ErrInvalidCredentials
	}
	iat, exp, err := issuedAndExpiryClaims(mc)
	if err != nil {
		return nil, apperr.ErrInvalidCredentials
	}

	return &Claims{
		JTI:       jti,
		Subject:   sub,
		Kind:      kind,
		IssuedAt:  iat,
		ExpiresAt: exp,
	}, nil
}

func adminSubjectFromClaims(mc jwt.MapClaims) (string, error) {
	sub, err := requiredStringClaim(mc, "sub")
	if err != nil || sub != adminSubject {
		return "", apperr.ErrInvalidCredentials
	}
	return sub, nil
}

func tokenKindFromClaims(mc jwt.MapClaims) (TokenKind, error) {
	kindStr, err := requiredStringClaim(mc, "kind")
	if err != nil {
		return "", apperr.ErrInvalidCredentials
	}
	switch TokenKind(kindStr) {
	case TokenKindAccess:
		return TokenKindAccess, nil
	case TokenKindRefresh:
		return TokenKindRefresh, nil
	default:
		return "", apperr.ErrInvalidCredentials
	}
}

func requiredStringClaim(mc jwt.MapClaims, name string) (string, error) {
	value, ok := mc[name].(string)
	if !ok || value == "" {
		return "", apperr.ErrInvalidCredentials
	}
	return value, nil
}

func issuedAndExpiryClaims(mc jwt.MapClaims) (time.Time, time.Time, error) {
	iat, ok := claimNumericDate(mc["iat"])
	if !ok {
		return time.Time{}, time.Time{}, apperr.ErrInvalidCredentials
	}
	exp, ok := claimNumericDate(mc["exp"])
	if !ok || !exp.After(iat) {
		return time.Time{}, time.Time{}, apperr.ErrInvalidCredentials
	}
	return iat, exp, nil
}

func claimNumericDate(value any) (time.Time, bool) {
	switch v := value.(type) {
	case float64:
		if v <= 0 {
			return time.Time{}, false
		}
		return time.Unix(int64(v), 0).UTC(), true
	case int64:
		if v <= 0 {
			return time.Time{}, false
		}
		return time.Unix(v, 0).UTC(), true
	case json.Number:
		unix, err := v.Int64()
		if err != nil || unix <= 0 {
			return time.Time{}, false
		}
		return time.Unix(unix, 0).UTC(), true
	default:
		return time.Time{}, false
	}
}
