package auth

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"

	"golang.org/x/crypto/bcrypt"

	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

type PasswordVerifier struct {
	stored []byte
}

var _ authusecase.PasswordVerifier = (*PasswordVerifier)(nil)

func NewPasswordVerifier(stored []byte) *PasswordVerifier {
	return &PasswordVerifier{stored: append([]byte(nil), stored...)}
}

func (v *PasswordVerifier) Verify(password string) bool {
	if v == nil {
		return false
	}
	if isBcryptHash(v.stored) {
		return bcrypt.CompareHashAndPassword(v.stored, []byte(password)) == nil
	}
	storedSum := sha256.Sum256(v.stored)
	suppliedSum := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(storedSum[:], suppliedSum[:]) == 1
}

func isBcryptHash(stored []byte) bool {
	return bytes.HasPrefix(stored, []byte("$2a$")) ||
		bytes.HasPrefix(stored, []byte("$2b$")) ||
		bytes.HasPrefix(stored, []byte("$2y$"))
}
