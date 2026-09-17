package presence_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	presenceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/presence"
)

func TestPausedPresenceContracts(t *testing.T) {
	if err := presenceusecase.ValidatePausedPresenceCommand(presenceusecase.PausedPresenceCommand{}); !errors.Is(err, presenceusecase.ErrInvalidPausedPresence) {
		t.Fatalf("ValidatePausedPresenceCommand() error = %v", err)
	}

	id := uuid.New()
	first := pausedomain.PausePresence{ID: id, TournamentID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New(), ParticipantID: uuid.New()}
	second := first
	second.State = pausedomain.PresenceStateDisconnected
	if !presenceusecase.SamePausePresenceIdentity(first, second) {
		t.Fatal("SamePausePresenceIdentity rejected equal durable identity")
	}
	second.ID = uuid.New()
	if presenceusecase.SamePausePresenceIdentity(first, second) {
		t.Fatal("SamePausePresenceIdentity accepted different durable identity")
	}
}
