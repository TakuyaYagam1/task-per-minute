package submission

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubmissionIntentDigestIsDomainSeparatedAndSensitiveToFlagBytes(t *testing.T) {
	t.Parallel()

	digest, err := SubmissionIntentDigest("accepted-flag")
	require.NoError(t, err)
	require.Equal(t,
		"4ae162b95a683007ef2656facbec9613d507fa12e6b78696de7c0559d2550372",
		hex.EncodeToString(digest[:]),
	)

	changed, err := SubmissionIntentDigest("accepted-flag ")
	require.NoError(t, err)
	require.NotEqual(t, digest, changed)

	_, err = SubmissionIntentDigest("")
	require.Error(t, err)
	_, err = SubmissionIntentDigest(string([]byte{0xff}))
	require.Error(t, err)
	_, err = SubmissionIntentDigest(string(make([]byte, maxFlagBytes+1)))
	require.Error(t, err)
	_, err = SubmissionIntentDigest(string(make([]byte, sha256.Size)))
	require.NoError(t, err)
}
