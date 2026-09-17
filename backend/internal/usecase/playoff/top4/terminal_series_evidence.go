package top4

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

var ErrInvalidTerminalSeriesEvidence = errors.New("invalid terminal Series evidence")

type TerminalSeriesEvidenceInput struct {
	Series               domain.Series
	OfficialResult       resultprojection.OfficialResultProjectionInput
	Projection           domain.ProjectionRevision
	Result               swissusecase.SeriesPointResult
	NoGameEvidenceDigest string
}

type terminalSeriesRecord struct {
	Series               domain.Series
	OfficialResult       resultprojection.OfficialResultProjectionInput
	Projection           domain.ProjectionRevision
	Result               swissusecase.SeriesPointResult
	NoGameEvidenceDigest string
}

type TerminalSeriesEvidence struct {
	record terminalSeriesRecord
}

type FinalSwissRound struct {
	RoundID     uuid.UUID
	RoundNumber int
	RevisionID  uuid.UUID
	LockProof   swissusecase.RoundLockProof
	Series      []TerminalSeriesEvidence
	Bye         *swissusecase.ByePointResult
}

func NewTerminalSeriesEvidence(
	input TerminalSeriesEvidenceInput,
) (TerminalSeriesEvidence, error) {
	official, err := input.OfficialResult.Clone()
	if err != nil {
		return TerminalSeriesEvidence{}, invalidTerminalSeriesEvidence("clone official result: %v", err)
	}
	evidence := TerminalSeriesEvidence{record: terminalSeriesRecord{
		Series: cloneTerminalSeries(input.Series), OfficialResult: official,
		Projection:           cloneFinalSwissDomainProjection(input.Projection),
		Result:               swissusecase.CloneSeriesPointResult(input.Result),
		NoGameEvidenceDigest: input.NoGameEvidenceDigest,
	}}
	if err := evidence.Validate(); err != nil {
		return TerminalSeriesEvidence{}, err
	}
	return evidence.Snapshot(), nil
}

func (e TerminalSeriesEvidence) Validate() error {
	record := e.record
	if err := preflightFinalSwissHead(record); err != nil {
		return invalidTerminalSeriesEvidence("invalid bounded record: %v", err)
	}
	if err := validateTerminalSeriesRecord(record); err != nil {
		return invalidTerminalSeriesEvidence("invalid terminal record: %v", err)
	}
	if record.OfficialResult.NoGame == nil {
		if record.NoGameEvidenceDigest != "" {
			return invalidTerminalSeriesEvidence("ordinary result has a no-game digest")
		}
		return nil
	}
	decoded, err := hex.DecodeString(record.NoGameEvidenceDigest)
	if err != nil || len(decoded) != sha256.Size {
		return invalidTerminalSeriesEvidence("invalid no-game evidence digest")
	}
	return nil
}

func validateTerminalSeriesRecord(record terminalSeriesRecord) error {
	createdAt := terminalSeriesEvidenceTime(record)
	round := finalSwissRound{
		RoundID: record.Result.RoundID, RoundNumber: record.Result.RoundNumber,
	}
	if record.OfficialResult.NoGame != nil {
		round.LockProof.WaveID = record.OfficialResult.NoGame.Scope.WaveID
	}
	if err := validateFinalSwissSeriesHead(finalSwissAuthority{
		TournamentID: record.Series.TournamentID,
		CreatedAt:    createdAt,
	}, round, record); err != nil {
		return err
	}
	if !validTerminalSeriesPointTime(
		record.Result.FirstEffectiveTime, record.Result.FirstAcceptedSolveTime,
	) || !validTerminalSeriesPointTime(
		record.Result.SecondEffectiveTime, record.Result.SecondAcceptedSolveTime,
	) {
		return finalSwissError("Series point timing is invalid")
	}
	switch record.Result.Label {
	case swissusecase.SeriesResultNoShow:
		if record.Result.WinnerID == nil {
			return finalSwissError("decisive Series point result has no winner")
		}
	case swissusecase.SeriesResultVoid:
		if record.Result.WinnerID != nil {
			return finalSwissError("void Series point result has a winner")
		}
	case swissusecase.SeriesResultPlayed:
		if record.Result.WinnerID == nil {
			return finalSwissError("decisive Series point result has no winner")
		}
	default:
		return finalSwissError("Series point result has an unknown label")
	}
	return nil
}

func terminalSeriesEvidenceTime(record terminalSeriesRecord) time.Time {
	createdAt := record.Projection.Revision().CreatedAt()
	if record.OfficialResult.NoGame != nil {
		if recordedAt := record.OfficialResult.NoGame.ResolvedAt; recordedAt.After(createdAt) {
			createdAt = recordedAt
		}
		return createdAt
	}
	if recordedAt := record.OfficialResult.Result.RecordedAt; recordedAt.After(createdAt) {
		return recordedAt
	}
	return createdAt
}

func validTerminalSeriesPointTime(effective time.Duration, accepted *time.Duration) bool {
	return effective >= 0 && (accepted == nil || *accepted >= 0 && *accepted <= effective)
}

func (e TerminalSeriesEvidence) Snapshot() TerminalSeriesEvidence {
	return TerminalSeriesEvidence{record: cloneFinalSwissSeriesHead(e.record)}
}

func (e TerminalSeriesEvidence) Series() domain.Series {
	return cloneTerminalSeries(e.record.Series)
}

func (e TerminalSeriesEvidence) OfficialResult() resultprojection.OfficialResultProjectionInput {
	clone, _ := e.record.OfficialResult.Clone()
	return clone
}

func (e TerminalSeriesEvidence) Projection() domain.ProjectionRevision {
	return cloneFinalSwissDomainProjection(e.record.Projection)
}

func (e TerminalSeriesEvidence) PointResult() swissusecase.SeriesPointResult {
	return swissusecase.CloneSeriesPointResult(e.record.Result)
}

func (e TerminalSeriesEvidence) NoGameEvidenceDigest() string {
	return e.record.NoGameEvidenceDigest
}

func terminalSeriesRecords(input []TerminalSeriesEvidence) []terminalSeriesRecord {
	records := make([]terminalSeriesRecord, len(input))
	for index, evidence := range input {
		records[index] = cloneFinalSwissSeriesHead(evidence.record)
	}
	return records
}

func invalidTerminalSeriesEvidence(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidTerminalSeriesEvidence, fmt.Sprintf(format, arguments...))
}
