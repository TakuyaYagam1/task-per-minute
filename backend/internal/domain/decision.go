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
	DecisionAlgorithmV1 = "hmac-sha256-order-v1"
	DecisionSeedSize    = 32
)

type DecisionPurpose string

const (
	DecisionPurposeCategory     DecisionPurpose = "category"
	DecisionPurposeDraftOrder   DecisionPurpose = "draft_order"
	DecisionPurposePairing      DecisionPurpose = "pairing"
	DecisionPurposeReserveOrder DecisionPurpose = "reserve_order"
	DecisionPurposeTask         DecisionPurpose = "task"
	DecisionPurposeWaveOrder    DecisionPurpose = "wave_order"
)

var (
	ErrInvalidDecisionEvidence = errors.New("invalid decision evidence")
	ErrDecisionReplayMismatch  = errors.New("decision replay mismatch")
)

type DecisionEvidence struct {
	ID               uuid.UUID
	Purpose          DecisionPurpose
	AlgorithmVersion string
	NormalizedInputs []string
	Seed             [DecisionSeedSize]byte
	Result           []string
	ReplayDigest     [sha256.Size]byte
	OwnerID          uuid.UUID
	DecidedAt        time.Time
}

type decisionRank struct {
	value  string
	digest [sha256.Size]byte
}

func (p DecisionPurpose) IsValid() bool {
	switch p {
	case DecisionPurposeCategory,
		DecisionPurposeDraftOrder,
		DecisionPurposePairing,
		DecisionPurposeReserveOrder,
		DecisionPurposeTask,
		DecisionPurposeWaveOrder:
		return true
	}
	return false
}

func NewDecisionEvidence(
	id uuid.UUID,
	purpose DecisionPurpose,
	algorithmVersion string,
	inputs []string,
	ownerID uuid.UUID,
	decidedAt time.Time,
) (DecisionEvidence, error) {
	normalized, err := NormalizeDecisionInputs(inputs)
	if err != nil {
		return DecisionEvidence{}, err
	}
	evidence := DecisionEvidence{
		ID:               id,
		Purpose:          purpose,
		AlgorithmVersion: algorithmVersion,
		NormalizedInputs: normalized,
		OwnerID:          ownerID,
		DecidedAt:        decidedAt.Round(0).UTC(),
	}
	if _, err := rand.Read(evidence.Seed[:]); err != nil {
		return DecisionEvidence{}, fmt.Errorf("%w: generate seed: %w", ErrInvalidDecisionEvidence, err)
	}
	if err := evidence.validateMetadata(); err != nil {
		return DecisionEvidence{}, err
	}
	evidence.Result, err = evidence.replayResult()
	if err != nil {
		return DecisionEvidence{}, err
	}
	evidence.ReplayDigest = evidence.replayDigest(evidence.Result)
	return evidence, nil
}

func NormalizeDecisionInputs(inputs []string) ([]string, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: empty inputs", ErrInvalidDecisionEvidence)
	}
	normalized := make([]string, len(inputs))
	for i, input := range inputs {
		value := strings.TrimSpace(input)
		if value == "" || !utf8.ValidString(value) {
			return nil, fmt.Errorf("%w: invalid input", ErrInvalidDecisionEvidence)
		}
		normalized[i] = value
	}
	sort.Strings(normalized)
	for i := 1; i < len(normalized); i++ {
		if normalized[i] == normalized[i-1] {
			return nil, fmt.Errorf("%w: duplicate normalized input %q", ErrInvalidDecisionEvidence, normalized[i])
		}
	}
	return normalized, nil
}

func (e DecisionEvidence) Validate() error {
	if err := e.validateMetadata(); err != nil {
		return err
	}
	normalized, err := NormalizeDecisionInputs(e.NormalizedInputs)
	if err != nil {
		return err
	}
	if !equalStrings(e.NormalizedInputs, normalized) {
		return fmt.Errorf("%w: inputs are not normalized and sorted", ErrInvalidDecisionEvidence)
	}
	if !sameStringSet(e.Result, e.NormalizedInputs) {
		return fmt.Errorf("%w: result is not a permutation of inputs", ErrInvalidDecisionEvidence)
	}
	expectedDigest := e.replayDigest(e.Result)
	if allZero(e.ReplayDigest[:]) || !hmac.Equal(e.ReplayDigest[:], expectedDigest[:]) {
		return ErrDecisionReplayMismatch
	}
	replayed, err := e.replayResult()
	if err != nil {
		return err
	}
	if !bytes.Equal(encodeDecisionStrings(e.Result), encodeDecisionStrings(replayed)) {
		return ErrDecisionReplayMismatch
	}
	return nil
}

func (e DecisionEvidence) Replay() ([]string, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return append([]string(nil), e.Result...), nil
}

func (e DecisionEvidence) CanonicalResult() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return encodeDecisionStrings(e.Result), nil
}

func (e DecisionEvidence) validateMetadata() error {
	if e.ID == uuid.Nil || e.OwnerID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidDecisionEvidence)
	}
	if !e.Purpose.IsValid() {
		return fmt.Errorf("%w: unknown purpose %q", ErrInvalidDecisionEvidence, e.Purpose)
	}
	if e.AlgorithmVersion != DecisionAlgorithmV1 {
		return fmt.Errorf("%w: unknown algorithm %q", ErrInvalidDecisionEvidence, e.AlgorithmVersion)
	}
	if e.DecidedAt.IsZero() || e.DecidedAt.Location() != time.UTC {
		return fmt.Errorf("%w: timestamp must be server UTC", ErrInvalidDecisionEvidence)
	}
	if allZero(e.Seed[:]) {
		return fmt.Errorf("%w: empty seed", ErrInvalidDecisionEvidence)
	}
	return nil
}

func (e DecisionEvidence) replayResult() ([]string, error) {
	if e.AlgorithmVersion != DecisionAlgorithmV1 {
		return nil, fmt.Errorf("%w: unknown algorithm %q", ErrInvalidDecisionEvidence, e.AlgorithmVersion)
	}
	context := e.canonicalContext()
	ranks := make([]decisionRank, len(e.NormalizedInputs))
	for i, value := range e.NormalizedInputs {
		mac := hmac.New(sha256.New, e.Seed[:])
		_, _ = mac.Write(context)
		writeDecisionField(mac, value)
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

func (e DecisionEvidence) canonicalContext() []byte {
	var buffer bytes.Buffer
	writeDecisionField(&buffer, e.AlgorithmVersion)
	writeDecisionField(&buffer, string(e.Purpose))
	writeDecisionField(&buffer, e.OwnerID.String())
	writeDecisionField(&buffer, e.DecidedAt.Format(time.RFC3339Nano))
	for _, input := range e.NormalizedInputs {
		writeDecisionField(&buffer, input)
	}
	return buffer.Bytes()
}

func (e DecisionEvidence) replayDigest(result []string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, e.Seed[:])
	_, _ = mac.Write(e.canonicalContext())
	writeDecisionField(mac, "recorded-result")
	for _, value := range result {
		writeDecisionField(mac, value)
	}
	var digest [sha256.Size]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

type decisionWriter interface {
	Write(data []byte) (int, error)
}

func writeDecisionField(writer decisionWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func encodeDecisionStrings(values []string) []byte {
	var buffer bytes.Buffer
	for _, value := range values {
		writeDecisionField(&buffer, value)
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
