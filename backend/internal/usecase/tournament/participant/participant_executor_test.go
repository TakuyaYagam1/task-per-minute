package participant

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestValidateResolvedAuthorityRejectsCancelledTournament(t *testing.T) {
	t.Parallel()

	actor := usecase.Identity{PlayerID: uuid.New()}
	tournamentID := uuid.New()
	authority := ParticipantCommandAuthority{
		TournamentID:         tournamentID,
		RosterID:             uuid.New(),
		PlayerID:             actor.PlayerID,
		ParticipantID:        uuid.New(),
		TournamentState:      domain.TournamentStateCancelled,
		ProjectionRevisionID: uuid.New(),
		ProjectionRevision:   4,
	}

	if err := validateResolvedAuthority(authority, actor, tournamentID, 4); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cancelled authority error = %v, want conflict", err)
	}
}

func TestValidateActiveAuthorityRejectsCompletedTournament(t *testing.T) {
	t.Parallel()

	actor := usecase.Identity{PlayerID: uuid.New()}
	tournamentID := uuid.New()
	authority := ParticipantCommandAuthority{
		TournamentID:         tournamentID,
		RosterID:             uuid.New(),
		PlayerID:             actor.PlayerID,
		ParticipantID:        uuid.New(),
		TournamentState:      domain.TournamentStateCompleted,
		ProjectionRevisionID: uuid.New(),
		ProjectionRevision:   5,
	}

	if err := validateActiveAuthority(authority, actor, tournamentID, 5); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("completed active authority error = %v, want conflict", err)
	}
	if err := validateResolvedAuthority(authority, actor, tournamentID, 5); err != nil {
		t.Fatalf("completed post-series authority error = %v, want nil", err)
	}
}
