package snapshot

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

func TestTournamentAdminSnapshotPauseIndexSelectsNormalRoot(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	waveID := uuid.New()
	startedAt := time.Date(2026, time.September, 6, 11, 0, 0, 0, time.UTC)
	header := tournamentAdminSnapshotHeaderState{tournament: usecase.TournamentView{
		ID: tournamentID, RosterID: rosterID, State: domain.TournamentStateTechnicalPause,
	}}
	roster := tournamentadmin.RosterView{ID: rosterID, TournamentID: tournamentID}
	waves := []tournamentadminexecution.WaveView{{Wave: domain.Wave{ID: waveID, TournamentID: tournamentID}}}
	row := tournamentAdminSnapshotPauseTestRow(tournamentID, rosterID, waveID, startedAt)

	index, err := tournamentAdminSnapshotPauses(
		[]sqlc.Pause{row},
		header,
		roster,
		waves,
		tournamentAdminSnapshotSeriesGraph{},
	)
	require.NoError(t, err)
	root, err := index.normalRoot(domain.TournamentStateTechnicalPause)
	require.NoError(t, err)
	require.NotNil(t, root)
	require.Equal(t, row.ID, root.ID)

	_, err = index.normalRoot(domain.TournamentStateSwiss)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentAdminSnapshotFrozenDeadline(t *testing.T) {
	t.Parallel()

	frozenAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	deadline := frozenAt.Add(30 * time.Second)
	value, ok := tournamentAdminSnapshotFrozenDeadline(
		gamepause.PauseDeadlineGame,
		uuid.New(),
		deadline,
		frozenAt,
		2,
	)
	require.True(t, ok)
	require.Equal(t, 30*time.Second, value.Remaining)
	require.Equal(t, deadline, value.OriginalDeadline)

	_, ok = tournamentAdminSnapshotFrozenDeadline(
		gamepause.PauseDeadlineGame,
		uuid.New(),
		frozenAt,
		frozenAt,
		1,
	)
	require.False(t, ok)
}

func TestTournamentAdminSnapshotReconnectRootSuffix(t *testing.T) {
	t.Parallel()

	require.True(t, tournamentAdminSnapshotReconnectRootSuffixValid(
		tournamentAdminSnapshotReconnectRootRange{count: 1, first: 2, last: 2},
		2,
	))
	require.True(t, tournamentAdminSnapshotReconnectRootSuffixValid(
		tournamentAdminSnapshotReconnectRootRange{},
		0,
	))
	require.False(t, tournamentAdminSnapshotReconnectRootSuffixValid(
		tournamentAdminSnapshotReconnectRootRange{count: 1, first: 2, last: 2},
		3,
	))
	require.False(t, tournamentAdminSnapshotReconnectRootSuffixValid(
		tournamentAdminSnapshotReconnectRootRange{count: 2, first: 1, last: 3},
		3,
	))
}

func tournamentAdminSnapshotPauseTestRow(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	waveID uuid.UUID,
	startedAt time.Time,
) sqlc.Pause {
	return sqlc.Pause{
		ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID,
		ScopeKind: "wave", ScopeID: waveID, WaveID: uuid.NullUUID{UUID: waveID, Valid: true},
		Depth: 0, Reason: string(gamepause.PauseReasonOperator),
		PausedFromState: string(domain.WaveStateActive), State: string(gamepause.PauseStateActive),
		CurrentRevisionID: uuid.New(), Revision: 1,
		StartedAt: tournamentAdminSnapshotTestTime(startedAt),
		CreatedAt: tournamentAdminSnapshotTestTime(startedAt),
		UpdatedAt: tournamentAdminSnapshotTestTime(startedAt),
	}
}
