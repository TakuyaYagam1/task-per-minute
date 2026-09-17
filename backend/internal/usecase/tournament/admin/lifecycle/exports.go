package lifecycle

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func ValidTournamentActionCommand(command TournamentActionCommand) bool {
	return validTournamentActionCommand(command)
}

func ValidTournamentView(view usecase.TournamentView, tournamentID uuid.UUID) bool {
	return validTournamentView(view, tournamentID)
}

func LifecycleActionState(action TournamentAction) (domain.TournamentState, bool) {
	return lifecycleActionState(action)
}

func ValidateLifecycleExecutionSnapshot(
	snapshot LifecycleExecutionSnapshot,
	authority LifecycleAuthority,
) error {
	return validateLifecycleExecutionSnapshot(snapshot, authority)
}
