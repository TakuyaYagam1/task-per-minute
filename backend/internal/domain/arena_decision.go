package domain

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	ArenaDecisionAlgorithmV1 = "hmac-sha256-order-v1"
	ArenaDecisionSeedSize    = 32
)

type ArenaDecisionPurpose string

const (
	ArenaDecisionPurposeCategory     ArenaDecisionPurpose = "category"
	ArenaDecisionPurposeDraftOrder   ArenaDecisionPurpose = "draft_order"
	ArenaDecisionPurposePairing      ArenaDecisionPurpose = "pairing"
	ArenaDecisionPurposeReserveOrder ArenaDecisionPurpose = "reserve_order"
	ArenaDecisionPurposeTask         ArenaDecisionPurpose = "task"
	ArenaDecisionPurposeWaveOrder    ArenaDecisionPurpose = "wave_order"
)

var (
	ErrInvalidArenaDecisionEvidence = errors.New("invalid arena decision evidence")
	ErrArenaDecisionReplayMismatch  = errors.New("arena decision replay mismatch")
)

type ArenaDecisionEvidence struct {
	ID               uuid.UUID
	Purpose          ArenaDecisionPurpose
	AlgorithmVersion string
	NormalizedInputs []string
	Seed             [ArenaDecisionSeedSize]byte
	Result           []string
	ReplayDigest     [sha256.Size]byte
	OwnerID          uuid.UUID
	DecidedAt        time.Time
}

type arenaDecisionRank struct {
	value  string
	digest [sha256.Size]byte
}

func (p ArenaDecisionPurpose) IsValid() bool {
	switch p {
	case ArenaDecisionPurposeCategory,
		ArenaDecisionPurposeDraftOrder,
		ArenaDecisionPurposePairing,
		ArenaDecisionPurposeReserveOrder,
		ArenaDecisionPurposeTask,
		ArenaDecisionPurposeWaveOrder:
		return true
	}
	return false
}

func NewArenaDecisionEvidence(
	id uuid.UUID,
	purpose ArenaDecisionPurpose,
	algorithmVersion string,
	inputs []string,
	ownerID uuid.UUID,
	decidedAt time.Time,
) (ArenaDecisionEvidence, error) {
	normalized, err := NormalizeArenaDecisionInputs(inputs)
	if err != nil {
		return ArenaDecisionEvidence{}, err
	}
	evidence := ArenaDecisionEvidence{
		ID:               id,
		Purpose:          purpose,
		AlgorithmVersion: algorithmVersion,
		NormalizedInputs: normalized,
		OwnerID:          ownerID,
		DecidedAt:        decidedAt.Round(0).UTC(),
	}
	if _, err := rand.Read(evidence.Seed[:]); err != nil {
		return ArenaDecisionEvidence{}, fmt.Errorf("%w: generate seed: %w", ErrInvalidArenaDecisionEvidence, err)
	}
	if err := evidence.validateMetadata(); err != nil {
		return ArenaDecisionEvidence{}, err
	}
	evidence.Result, err = evidence.replayResult()
	if err != nil {
		return ArenaDecisionEvidence{}, err
	}
	evidence.ReplayDigest = evidence.replayDigest(evidence.Result)
	return evidence, nil
}

func NormalizeArenaDecisionInputs(inputs []string) ([]string, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: empty inputs", ErrInvalidArenaDecisionEvidence)
	}
	normalized := make([]string, len(inputs))
	for i, input := range inputs {
		value := strings.TrimSpace(input)
		if value == "" || !utf8.ValidString(value) {
			return nil, fmt.Errorf("%w: invalid input", ErrInvalidArenaDecisionEvidence)
		}
		normalized[i] = value
	}
	sort.Strings(normalized)
	for i := 1; i < len(normalized); i++ {
		if normalized[i] == normalized[i-1] {
			return nil, fmt.Errorf("%w: duplicate normalized input %q", ErrInvalidArenaDecisionEvidence, normalized[i])
		}
	}
	return normalized, nil
}

func (e ArenaDecisionEvidence) Validate() error {
	if err := e.validateMetadata(); err != nil {
		return err
	}
	normalized, err := NormalizeArenaDecisionInputs(e.NormalizedInputs)
	if err != nil {
		return err
	}
	if !equalStrings(e.NormalizedInputs, normalized) {
		return fmt.Errorf("%w: inputs are not normalized and sorted", ErrInvalidArenaDecisionEvidence)
	}
	if !sameStringSet(e.Result, e.NormalizedInputs) {
		return fmt.Errorf("%w: result is not a permutation of inputs", ErrInvalidArenaDecisionEvidence)
	}
	expectedDigest := e.replayDigest(e.Result)
	if allZero(e.ReplayDigest[:]) || !hmac.Equal(e.ReplayDigest[:], expectedDigest[:]) {
		return ErrArenaDecisionReplayMismatch
	}
	replayed, err := e.replayResult()
	if err != nil {
		return err
	}
	if !bytes.Equal(encodeArenaDecisionStrings(e.Result), encodeArenaDecisionStrings(replayed)) {
		return ErrArenaDecisionReplayMismatch
	}
	return nil
}

func (e ArenaDecisionEvidence) Replay() ([]string, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return append([]string(nil), e.Result...), nil
}

func (e ArenaDecisionEvidence) CanonicalResult() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return encodeArenaDecisionStrings(e.Result), nil
}

func (e ArenaDecisionEvidence) validateMetadata() error {
	if e.ID == uuid.Nil || e.OwnerID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidArenaDecisionEvidence)
	}
	if !e.Purpose.IsValid() {
		return fmt.Errorf("%w: unknown purpose %q", ErrInvalidArenaDecisionEvidence, e.Purpose)
	}
	if e.AlgorithmVersion != ArenaDecisionAlgorithmV1 {
		return fmt.Errorf("%w: unknown algorithm %q", ErrInvalidArenaDecisionEvidence, e.AlgorithmVersion)
	}
	if e.DecidedAt.IsZero() || e.DecidedAt.Location() != time.UTC {
		return fmt.Errorf("%w: timestamp must be server UTC", ErrInvalidArenaDecisionEvidence)
	}
	if allZero(e.Seed[:]) {
		return fmt.Errorf("%w: empty seed", ErrInvalidArenaDecisionEvidence)
	}
	return nil
}

func (e ArenaDecisionEvidence) replayResult() ([]string, error) {
	if e.AlgorithmVersion != ArenaDecisionAlgorithmV1 {
		return nil, fmt.Errorf("%w: unknown algorithm %q", ErrInvalidArenaDecisionEvidence, e.AlgorithmVersion)
	}
	context := e.canonicalContext()
	ranks := make([]arenaDecisionRank, len(e.NormalizedInputs))
	for i, value := range e.NormalizedInputs {
		mac := hmac.New(sha256.New, e.Seed[:])
		_, _ = mac.Write(context)
		writeArenaDecisionField(mac, value)
		copy(ranks[i].digest[:], mac.Sum(nil))
		ranks[i].value = value
	}
	sort.Slice(ranks, func(i, j int) bool {
		comparison := bytes.Compare(ranks[i].digest[:], ranks[j].digest[:])
		if comparison == 0 {
			return ranks[i].value < ranks[j].value
		}
		return comparison < 0
	})
	result := make([]string, len(ranks))
	for i, rank := range ranks {
		result[i] = rank.value
	}
	return result, nil
}

func (e ArenaDecisionEvidence) canonicalContext() []byte {
	var buffer bytes.Buffer
	writeArenaDecisionField(&buffer, e.AlgorithmVersion)
	writeArenaDecisionField(&buffer, string(e.Purpose))
	writeArenaDecisionField(&buffer, e.OwnerID.String())
	writeArenaDecisionField(&buffer, e.DecidedAt.Format(time.RFC3339Nano))
	for _, input := range e.NormalizedInputs {
		writeArenaDecisionField(&buffer, input)
	}
	return buffer.Bytes()
}

func (e ArenaDecisionEvidence) replayDigest(result []string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, e.Seed[:])
	_, _ = mac.Write(e.canonicalContext())
	writeArenaDecisionField(mac, "recorded-result")
	for _, value := range result {
		writeArenaDecisionField(mac, value)
	}
	var digest [sha256.Size]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

type arenaDecisionWriter interface {
	Write([]byte) (int, error)
}

func writeArenaDecisionField(writer arenaDecisionWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func encodeArenaDecisionStrings(values []string) []byte {
	var buffer bytes.Buffer
	for _, value := range values {
		writeArenaDecisionField(&buffer, value)
	}
	return buffer.Bytes()
}

func equalStrings(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i] != second[i] {
			return false
		}
	}
	return true
}

func sameStringSet(first, second []string) bool {
	firstCopy := append([]string(nil), first...)
	secondCopy := append([]string(nil), second...)
	sort.Strings(firstCopy)
	sort.Strings(secondCopy)
	return equalStrings(firstCopy, secondCopy)
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
