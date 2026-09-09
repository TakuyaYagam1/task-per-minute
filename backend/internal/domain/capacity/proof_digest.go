package capacity

import (
	"crypto/sha256"
	"encoding/hex"
)

// ValidProofDigest reports whether value is a canonical SHA-256 proof digest.
func ValidProofDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
