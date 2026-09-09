package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

const (
	jwtIssuer   = "task-per-minute-backend"
	jwtAudience = "task-per-minute-admin"
)

type JWTConfig struct {
	Secret          []byte
	ClockSkewLeeway time.Duration
	Now             func() time.Time
}

type JWTCodec struct {
	secret          []byte
	clockSkewLeeway time.Duration
	now             func() time.Time
}

var _ authusecase.TokenCodec = (*JWTCodec)(nil)

func NewJWTCodec(config JWTConfig) *JWTCodec {
	leeway := config.ClockSkewLeeway
	if leeway <= 0 {
		leeway = authusecase.DefaultClockSkewLeeway
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &JWTCodec{
		secret:          append([]byte(nil), config.Secret...),
		clockSkewLeeway: leeway,
		now:             now,
	}
}

func (c *JWTCodec) Issue(
	subject string,
	kind authusecase.TokenKind,
	issuedAt time.Time,
	expiresAt time.Time,
) (string, error) {
	claims := jwt.MapClaims{
		"iss":  jwtIssuer,
		"aud":  jwtAudience,
		"sub":  subject,
		"kind": string(kind),
		"iat":  issuedAt.Unix(),
		"exp":  expiresAt.Unix(),
		"jti":  uuid.NewString(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(c.secret)
	if err != nil {
		return "", fmt.Errorf("JWTCodec - Issue - SignedString: %w", err)
	}
	return signed, nil
}

func (c *JWTCodec) Parse(token string) (*authusecase.Claims, error) {
	parsed, err := jwt.Parse(
		token,
		c.key,
		jwt.WithTimeFunc(c.now),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithAudience(jwtAudience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(c.clockSkewLeeway),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, domain.ErrTokenExpired
		}
		return nil, domain.ErrInvalidCredentials
	}
	if !parsed.Valid {
		return nil, domain.ErrInvalidCredentials
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, domain.ErrInvalidCredentials
	}
	return claimsFromMap(claims)
}

func (c *JWTCodec) key(token *jwt.Token) (any, error) {
	if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
		return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
	}
	return c.secret, nil
}

func claimsFromMap(claims jwt.MapClaims) (*authusecase.Claims, error) {
	subject, err := requiredStringClaim(claims, "sub")
	if err != nil {
		return nil, domain.ErrInvalidCredentials
	}
	kindValue, err := requiredStringClaim(claims, "kind")
	if err != nil {
		return nil, domain.ErrInvalidCredentials
	}
	kind := authusecase.TokenKind(kindValue)
	if kind != authusecase.TokenKindAccess && kind != authusecase.TokenKindRefresh {
		return nil, domain.ErrInvalidCredentials
	}
	jti, err := requiredStringClaim(claims, "jti")
	if err != nil {
		return nil, domain.ErrInvalidCredentials
	}
	issuedAt, ok := numericDate(claims["iat"])
	if !ok {
		return nil, domain.ErrInvalidCredentials
	}
	expiresAt, ok := numericDate(claims["exp"])
	if !ok || !expiresAt.After(issuedAt) {
		return nil, domain.ErrInvalidCredentials
	}
	return &authusecase.Claims{
		JTI:       jti,
		Subject:   subject,
		Kind:      kind,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

func requiredStringClaim(claims jwt.MapClaims, name string) (string, error) {
	value, ok := claims[name].(string)
	if !ok || value == "" {
		return "", domain.ErrInvalidCredentials
	}
	return value, nil
}

func numericDate(value any) (time.Time, bool) {
	switch date := value.(type) {
	case float64:
		if date <= 0 {
			return time.Time{}, false
		}
		return time.Unix(int64(date), 0).UTC(), true
	case int64:
		if date <= 0 {
			return time.Time{}, false
		}
		return time.Unix(date, 0).UTC(), true
	case json.Number:
		unix, err := date.Int64()
		if err != nil || unix <= 0 {
			return time.Time{}, false
		}
		return time.Unix(unix, 0).UTC(), true
	default:
		return time.Time{}, false
	}
}
