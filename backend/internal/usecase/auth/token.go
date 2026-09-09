package auth

import "time"

type TokenKind string

const (
	TokenKindAccess  TokenKind = "access"
	TokenKindRefresh TokenKind = "refresh"
)

type Claims struct {
	JTI       string
	Subject   string
	Kind      TokenKind
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}
