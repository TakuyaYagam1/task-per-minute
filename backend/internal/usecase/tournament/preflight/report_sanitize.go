package preflight

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

func safeRuntimeRevision(value string) string {
	if normalized, ok := normalizeRuntimeRevision(value); ok {
		return normalized
	}
	return "invalid#" + safeValueDigest(value)
}

func safeCapacityProofDigest(value string) string {
	if capacity.ValidProofDigest(value) {
		return strings.ToLower(value)
	}
	return "invalid#" + safeValueDigest(value)
}

func safeTypedValue(value string, valid bool) string {
	if valid {
		return value
	}
	return "invalid#" + safeValueDigest(value)
}

func safeValueDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func preflightReportError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidReport, message)
}
