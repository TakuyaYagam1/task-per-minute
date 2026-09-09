package pause

import (
	"errors"

	"github.com/google/uuid"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

var ErrInvalidScope = errors.New("invalid pause scope")

type ScopeKind string

const ScopeWave ScopeKind = "wave"

type GraphScope struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
	WaveID       uuid.UUID
	Authority    authoritydomain.Identity
}

func (scope GraphScope) Validate() error {
	if scope.TournamentID == uuid.Nil || scope.RosterID == uuid.Nil ||
		scope.WaveID == uuid.Nil || scope.Authority.Validate() != nil ||
		scope.Authority.TournamentID != scope.TournamentID {
		return ErrInvalidScope
	}
	return nil
}
