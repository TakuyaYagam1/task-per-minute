package auth_test

import (
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

const (
	testSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	testPass   = "p@ssw0rd"
	accessTTL  = 15 * time.Minute
	refreshTTL = 168 * time.Hour
)

type authFixtureConfig struct {
	Secret        []byte
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	AdminPassword []byte
}

func newAuthCfg() authFixtureConfig {
	return authFixtureConfig{
		Secret:        []byte(testSecret),
		AccessTTL:     accessTTL,
		RefreshTTL:    refreshTTL,
		AdminPassword: []byte(testPass),
	}
}

func newAuthUseCase(
	cfg authFixtureConfig,
	clock authusecase.Clock,
	revocations authusecase.RevocationStore,
) *authusecase.UseCase {
	return authusecase.NewUseCase(authusecase.Config{
		AccessTTL:  cfg.AccessTTL,
		RefreshTTL: cfg.RefreshTTL,
	}, clock, revocations, auth.NewJWTCodec(auth.JWTConfig{
		Secret: cfg.Secret,
		Now:    clock.Now,
	}), auth.NewPasswordVerifier(cfg.AdminPassword))
}

func newStubClock(t *testing.T, now time.Time) *authmocks.MockClock {
	t.Helper()
	clock := authmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func newMutableClock(t *testing.T, now time.Time) (*authmocks.MockClock, func(time.Time)) {
	t.Helper()
	var mu sync.RWMutex
	current := now
	clock := authmocks.NewMockClock(t)
	clock.EXPECT().Now().RunAndReturn(func() time.Time {
		mu.RLock()
		defer mu.RUnlock()
		return current
	}).Maybe()
	set := func(next time.Time) {
		mu.Lock()
		defer mu.Unlock()
		current = next
	}
	return clock, set
}

// signWithKind mints a HS256-signed JWT with an arbitrary `kind` claim. It
// lets the suite assert that the parser rejects values outside the
// {access,refresh} enum even when the signature itself is valid.
func signWithKind(t *testing.T, secret []byte, kind string, iat, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":  "task-per-minute-backend",
		"aud":  "task-per-minute-admin",
		"sub":  "admin",
		"kind": kind,
		"iat":  iat.Unix(),
		"exp":  exp.Unix(),
		"jti":  uuid.NewString(),
	})
	signed, err := tok.SignedString(secret)
	require.NoError(t, err)
	return signed
}

func signWithSubject(t *testing.T, secret []byte, sub string, kind authusecase.TokenKind, iat, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":  "task-per-minute-backend",
		"aud":  "task-per-minute-admin",
		"sub":  sub,
		"kind": string(kind),
		"iat":  iat.Unix(),
		"exp":  exp.Unix(),
		"jti":  uuid.NewString(),
	})
	signed, err := tok.SignedString(secret)
	require.NoError(t, err)
	return signed
}

func signWithClaims(t *testing.T, secret []byte, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	signed, err := tok.SignedString(secret)
	require.NoError(t, err)
	return signed
}

func validAdminClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":  "task-per-minute-backend",
		"aud":  "task-per-minute-admin",
		"sub":  "admin",
		"kind": string(authusecase.TokenKindAccess),
		"iat":  now.Unix(),
		"exp":  now.Add(accessTTL).Unix(),
		"jti":  uuid.NewString(),
	}
}
