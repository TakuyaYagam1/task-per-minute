package correction

import (
	"time"

	cutoffusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/cutoff"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

const (
	maxCorrectionCutoffEvents    = 4096
	maxCorrectionDAGProjections  = 512
	maxCorrectionDAGDependencies = 2048
	maxCorrectionDAGResults      = 512
	maxCorrectionDAGPayloadBytes = 512 << 10
	maxCorrectionAuthorityIDs    = 8192
	maxCorrectionNoGameAttempts  = 2048
	minCorrectionYear            = 2000
	maxCorrectionYear            = 2200
)

type CutoffKind = cutoffusecase.CutoffKind
type CutoffEvent = cutoffusecase.CutoffEvent
type CutoffInput = cutoffusecase.CutoffInput
type Cutoff = cutoffusecase.Cutoff
type RejectionCode = cutoffusecase.RejectionCode
type Error = cutoffusecase.Error

const (
	CutoffWaveStarted     = cutoffusecase.CutoffWaveStarted
	CutoffTaskDelivered   = cutoffusecase.CutoffTaskDelivered
	CutoffNoShowRecorded  = cutoffusecase.CutoffNoShowRecorded
	CutoffForfeitRecorded = cutoffusecase.CutoffForfeitRecorded
	CutoffGoldenAllocated = cutoffusecase.CutoffGoldenAllocated

	RejectionMalformed       = cutoffusecase.RejectionMalformed
	RejectionStale           = cutoffusecase.RejectionStale
	RejectionIncomplete      = cutoffusecase.RejectionIncomplete
	RejectionIdentityAlias   = cutoffusecase.RejectionIdentityAlias
	RejectionTerminal        = cutoffusecase.RejectionTerminal
	RejectionCrossTournament = cutoffusecase.RejectionCrossTournament
	RejectionCutoff          = cutoffusecase.RejectionCutoff
)

var (
	ErrInvalid = cutoffusecase.ErrInvalid
	ErrCutoff  = cutoffusecase.ErrCutoff
)

func Code(err error) RejectionCode {
	return cutoffusecase.Code(err)
}

func EvaluateCutoff(input CutoffInput) (Cutoff, error) {
	return cutoffusecase.EvaluateCutoff(input)
}

func rejectCorrection(code RejectionCode, cause error, detail string) error {
	return cutoffusecase.Reject(code, cause, detail)
}

func preflightCorrectionDAGResults(dag resultprojection.RevisionDAG) error {
	return cutoffusecase.PreflightDAGResults(dag)
}

func preflightCorrectionCutoff(input CutoffInput, snapshot resultprojection.RevisionDAGSnapshot) error {
	return cutoffusecase.PreflightCutoff(input, snapshot)
}

func evaluateCorrectionCutoffPrepared(
	input CutoffInput,
	snapshot resultprojection.RevisionDAGSnapshot,
) (Cutoff, error) {
	return cutoffusecase.EvaluatePrepared(input, snapshot)
}

func validCorrectionCutoffKind(kind CutoffKind) bool {
	switch kind {
	case CutoffWaveStarted,
		CutoffTaskDelivered,
		CutoffNoShowRecorded,
		CutoffForfeitRecorded,
		CutoffGoldenAllocated:
		return true
	default:
		return false
	}
}

func validCorrectionTime(value time.Time) bool {
	return validCorrectionServerTime(value) && value.Year() >= minCorrectionYear && value.Year() <= maxCorrectionYear
}

func canonicalCorrectionTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}
