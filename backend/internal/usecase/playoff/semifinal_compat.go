package playoff

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	semifinalusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff/semifinal"
)

var (
	ErrInvalidSemifinalBracket     = semifinalusecase.ErrInvalidSemifinalBracket
	ErrInvalidSemifinalAdvancement = semifinalusecase.ErrInvalidSemifinalAdvancement
	ErrInvalidSemifinalFlow        = semifinalusecase.ErrInvalidSemifinalFlow
	ErrSemifinalFlowConflict       = semifinalusecase.ErrSemifinalFlowConflict
	ErrSemifinalFlowCommandReuse   = semifinalusecase.ErrSemifinalFlowCommandReuse
)

type SemifinalWinnerPath = semifinalusecase.SemifinalWinnerPath
type SemifinalLoserPath = semifinalusecase.SemifinalLoserPath
type SemifinalMatch = semifinalusecase.SemifinalMatch
type SemifinalBracketCommand = semifinalusecase.SemifinalBracketCommand
type SemifinalBracket = semifinalusecase.SemifinalBracket
type SemifinalAdvancementResult = semifinalusecase.SemifinalAdvancementResult
type SemifinalAdvancementAuthority = semifinalusecase.SemifinalAdvancementAuthority
type SemifinalAdvancement = semifinalusecase.SemifinalAdvancement
type SemifinalFlowInput = semifinalusecase.SemifinalFlowInput
type SemifinalFlow = semifinalusecase.SemifinalFlow

const (
	SemifinalWinnerToFinal   = semifinalusecase.SemifinalWinnerToFinal
	SemifinalLoserEliminated = semifinalusecase.SemifinalLoserEliminated
)

func PlanStrengthMatchedSemifinals(command SemifinalBracketCommand) (SemifinalBracket, error) {
	return semifinalusecase.PlanStrengthMatchedSemifinals(command)
}

func AdvanceSemifinalResults(
	current SemifinalAdvancement,
	bracket SemifinalBracket,
	series []domain.Series,
) (SemifinalAdvancement, bool, error) {
	return semifinalusecase.AdvanceSemifinalResults(current, bracket, series)
}

func AdvanceSemifinalEvidence(
	current SemifinalAdvancement,
	authority SemifinalAdvancementAuthority,
	series []domain.Series,
) (SemifinalAdvancement, bool, error) {
	return semifinalusecase.AdvanceSemifinalEvidence(current, authority, series)
}

func MaterializeSemifinals(
	existing *SemifinalFlow,
	input SemifinalFlowInput,
) (SemifinalFlow, bool, error) {
	return semifinalusecase.MaterializeSemifinals(existing, input)
}

func cloneSemifinalMatches(input []SemifinalMatch) []SemifinalMatch {
	return semifinalusecase.CloneSemifinalMatches(input)
}
