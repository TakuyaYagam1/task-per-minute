package correction

import cutoffusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/cutoff"

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

func CutoffKindOf(err error) CutoffKind {
	return cutoffusecase.Kind(err)
}

func RejectionDetail(err error) string {
	return cutoffusecase.Detail(err)
}

func RejectCutoff(kind CutoffKind) error {
	return cutoffusecase.RejectCutoff(kind)
}

func EvaluateCutoff(input CutoffInput) (Cutoff, error) {
	return cutoffusecase.EvaluateCutoff(input)
}
