package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
)

// ensurePreStartSwissRoundProof keeps terminal result and recovery workflows
// source-compatible while the proof implementation lives in wavestart.
func ensurePreStartSwissRoundProof(
	ctx context.Context,
	tx *TxManager,
	tournamentID, seriesID uuid.UUID,
	at time.Time,
	origin swissRoundProofOrigin,
) error {
	return wavestartrepo.EnsurePreStartSwissRoundProofForCommand(
		ctx, tx, tournamentID, seriesID, at, origin.mode, origin.commandID,
	)
}
