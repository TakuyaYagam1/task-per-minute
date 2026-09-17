package progression

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// TournamentProgressionPostgres owns the durable stage progression boundary.
// Every method joins the caller's outer lifecycle transaction through TxManager.
type TournamentProgressionPostgres struct {
	tx *db.TxManager
}

var (
	_ tournamentprogression.Repository                  = (*TournamentProgressionPostgres)(nil)
	_ tournamentprogression.SwissTerminalEvidenceReader = (*TournamentProgressionPostgres)(nil)
	_ tournamentprogression.Transitioner                = (*TournamentProgressionPostgres)(nil)
	_ tournamentprogression.PlayoffProjectionPublisher  = (*TournamentProgressionPostgres)(nil)
)

func NewTournamentProgressionPostgres(tx *db.TxManager) *TournamentProgressionPostgres {
	return &TournamentProgressionPostgres{tx: tx}
}

func (r *TournamentProgressionPostgres) TransitionStage(
	ctx context.Context,
	command tournamentprogression.TransitionCommand,
) (usecase.TournamentView, bool, error) {
	if r == nil || r.tx == nil || ctx == nil || command.TournamentID == uuid.Nil ||
		command.ExpectedRevision < 1 || !command.NextState.IsValid() {
		return usecase.TournamentView{}, false, domain.ErrValidation
	}

	return r.transitionStage(ctx, command)
}

// Keep imports anchored while the remaining progression methods are added in
// their dedicated files. The symbols are part of the public port contract.
var (
	_ playoff.ProgressionSwissInput
)
