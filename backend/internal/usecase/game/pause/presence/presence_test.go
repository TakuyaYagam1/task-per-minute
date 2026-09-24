package presence_test

import (
	"errors"
	"testing"
	"time"

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

func TestClonePausedPresenceAuthorityPreservesEmptyEvidenceCollections(t *testing.T) {
	authority := presenceusecase.PausedPresenceAuthority{
		Reconnect: make([]pausedomain.PauseReconnectInterval, 0),
		Counters:  make([]pausedomain.PauseReconnectCounter, 0),
	}

	cloned := presenceusecase.ClonePausedPresenceAuthority(authority)
	if cloned.Reconnect == nil {
		t.Error("ClonePausedPresenceAuthority turned an explicitly empty Reconnect slice into nil")
	}
	if cloned.Counters == nil {
		t.Error("ClonePausedPresenceAuthority turned an explicitly empty Counters slice into nil")
	}
}

func TestClonePausedPresenceAuthorityDeepCopiesPopulatedCollections(t *testing.T) {
	closedAt := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	authority := presenceusecase.PausedPresenceAuthority{
		Reconnect: []pausedomain.PauseReconnectInterval{{ClosedAt: &closedAt}},
		Counters:  []pausedomain.PauseReconnectCounter{{}},
	}

	cloned := presenceusecase.ClonePausedPresenceAuthority(authority)
	if &cloned.Reconnect[0] == &authority.Reconnect[0] {
		t.Fatal("ClonePausedPresenceAuthority reused the Reconnect slice storage")
	}
	if cloned.Reconnect[0].ClosedAt == authority.Reconnect[0].ClosedAt {
		t.Fatal("ClonePausedPresenceAuthority reused a nested Reconnect timestamp")
	}
	if &cloned.Counters[0] == &authority.Counters[0] {
		t.Fatal("ClonePausedPresenceAuthority reused the Counters slice storage")
	}
}
