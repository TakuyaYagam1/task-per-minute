package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPasswordHasherUsesArgon2idAndVerifiesPassword(t *testing.T) {
	hasher := NewPasswordHasher()
	password := "long-passphrase-with-enough-entropy"

	first, err := hasher.Hash(password)
	require.NoError(t, err)
	second, err := hasher.Hash(password)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(first, "$argon2id$v=19$"))
	require.NotEqual(t, first, second, "each password hash must use a fresh salt")

	valid, err := hasher.Verify(password, first)
	require.NoError(t, err)
	require.True(t, valid)

	valid, err = hasher.Verify(password+"x", first)
	require.NoError(t, err)
	require.False(t, valid)
}

func TestPasswordHasherRejectsMalformedOrExpensiveParameters(t *testing.T) {
	hasher := NewPasswordHasher()
	encoded, err := hasher.Hash("long-passphrase-with-enough-entropy")
	require.NoError(t, err)

	for _, candidate := range []string{
		"$argon2id$v=19$m=4294967295,t=2,p=1$bad$bad",
		strings.Replace(encoded, "m=19456", "m=194560000", 1),
		strings.Replace(encoded, "t=2", "t=2000000", 1),
	} {
		valid, err := hasher.Verify("long-passphrase-with-enough-entropy", candidate)
		require.ErrorIs(t, err, ErrMalformedPlayerPasswordHash)
		require.False(t, valid)
	}
}
