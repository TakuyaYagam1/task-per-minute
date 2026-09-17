package playoff

import semifinalusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff/semifinal"

var ErrInvalidSemifinalBracket = semifinalusecase.ErrInvalidSemifinalBracket

type SemifinalWinnerPath = semifinalusecase.SemifinalWinnerPath
type SemifinalLoserPath = semifinalusecase.SemifinalLoserPath
type SemifinalMatch = semifinalusecase.SemifinalMatch
type SemifinalBracketCommand = semifinalusecase.SemifinalBracketCommand
type SemifinalBracket = semifinalusecase.SemifinalBracket

const (
	SemifinalWinnerToFinal   = semifinalusecase.SemifinalWinnerToFinal
	SemifinalLoserEliminated = semifinalusecase.SemifinalLoserEliminated
)

func PlanStrengthMatchedSemifinals(command SemifinalBracketCommand) (SemifinalBracket, error) {
	return semifinalusecase.PlanStrengthMatchedSemifinals(command)
}

func cloneSemifinalMatches(input []SemifinalMatch) []SemifinalMatch {
	return semifinalusecase.CloneSemifinalMatches(input)
}
