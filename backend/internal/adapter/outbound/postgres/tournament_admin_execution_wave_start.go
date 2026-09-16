package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// Wave-start persistence lives in the execution child package. These root
// methods preserve the historical facade used by bootstrap and use cases.
func (r *TournamentAdminExecutionPostgres) ReadWaveStartTime(ctx context.Context) (time.Time, error) {
	if r == nil || r.inner == nil {
		return time.Time{}, domain.ErrValidation
	}
	return r.inner.ReadWaveStartTime(ctx)
}

func (r *TournamentAdminExecutionPostgres) LoadWaveStartAuthority(
	ctx context.Context,
	scope gameusecase.StartScope,
) (gameusecase.StartAuthority, error) {
	if r == nil || r.inner == nil {
		return gameusecase.StartAuthority{}, domain.ErrValidation
	}
	return r.inner.LoadWaveStartAuthority(ctx, scope)
}

func (r *TournamentAdminExecutionPostgres) CommitWaveStart(
	ctx context.Context,
	record gameusecase.StartRecord,
) (*gameusecase.StartRecord, bool, error) {
	if r == nil || r.inner == nil {
		return nil, false, domain.ErrValidation
	}
	return r.inner.CommitWaveStart(ctx, record)
}

// swissRoundProofOrigin retains the private root callback shape used by
// result and recovery workflows during the child-package migration.
type swissRoundProofOrigin struct {
	mode      string
	commandID uuid.UUID
}

func ensureSwissRoundLockProof(
	ctx context.Context,
	querier *sqlc.Queries,
	proof swissusecase.RoundLockProof,
	lockedAt time.Time,
	origins ...swissRoundProofOrigin,
) error {
	converted := make([]wavestartrepo.SwissRoundProofOrigin, len(origins))
	for index, origin := range origins {
		converted[index] = wavestartrepo.SwissRoundProofOrigin{Mode: origin.mode, CommandID: origin.commandID}
	}
	return wavestartrepo.EnsureSwissRoundLockProof(ctx, querier, proof, lockedAt, converted...)
}
