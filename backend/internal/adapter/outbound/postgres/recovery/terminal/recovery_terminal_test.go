package terminal

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func TestRecoveryReconnectArtifactKindsMatchSettlementStage(t *testing.T) {
	t.Parallel()

	record := &gameusecase.ReconnectRecord{}
	require.ElementsMatch(t, []domain.ArtifactKind{
		domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
	}, recoveryReconnectArtifactKinds(record))

	record.SeriesResultRevision = &gameusecase.SeriesRevision{}
	require.ElementsMatch(t, []domain.ArtifactKind{
		domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
		domain.ArtifactKindStandings,
		domain.ArtifactKindSeriesResult,
	}, recoveryReconnectArtifactKinds(record))
}

func TestRecoveryNoShowArtifactKindsIncludeStandingsOnlyForCompletedSeries(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t, []domain.ArtifactKind{
		domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
		domain.ArtifactKindSeriesResult,
	}, recoveryNoShowArtifactKinds(domain.SeriesStateCancelled))
	require.ElementsMatch(t, []domain.ArtifactKind{
		domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
		domain.ArtifactKindStandings,
		domain.ArtifactKindSeriesResult,
	}, recoveryNoShowArtifactKinds(domain.SeriesStateCompleted))
}

func TestRecoveryReceiptRejectsUnexpectedIdentityForDeadlineKind(t *testing.T) {
	t.Parallel()

	ids := recoveryReceiptTestIDs()
	resolvedAt := pgtype.Timestamptz{Time: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC), Valid: true}
	tests := []struct {
		name     string
		deadline recoveryusecase.PendingDeadline
		receipt  sqlc.DeadlineTransitionReceipt
	}{
		{
			name: "game receipt carries participant",
			deadline: recoveryusecase.PendingDeadline{
				Kind: recoveryusecase.DeadlineKindGame, ID: ids.deadlineID, ExpectedRevision: 3,
				TournamentID: ids.tournamentID, RosterID: ids.rosterID, WaveID: ids.waveID,
				SeriesID: ids.seriesID, GameID: ids.gameID,
			},
			receipt: sqlc.DeadlineTransitionReceipt{
				DeadlineID: ids.deadlineID, ExpectedDeadlineRevision: 3,
				TournamentID: ids.tournamentID, RosterID: ids.rosterID, WaveID: ids.waveID,
				TransitionKind: "game_timeout_replay", SeriesID: nullableUUIDValue(ids.seriesID),
				GameAttemptID: nullableUUIDValue(ids.gameID), ParticipantID: nullableUUIDValue(ids.participantID),
				ResolvedAt: resolvedAt,
			},
		},
		{
			name: "ready window receipt carries participant",
			deadline: recoveryusecase.PendingDeadline{
				Kind: recoveryusecase.DeadlineKindReadyWindow, ID: ids.deadlineID, ExpectedRevision: 3,
				TournamentID: ids.tournamentID, RosterID: ids.rosterID, WaveID: ids.waveID,
			},
			receipt: sqlc.DeadlineTransitionReceipt{
				DeadlineID: ids.deadlineID, ExpectedDeadlineRevision: 3,
				TournamentID: ids.tournamentID, RosterID: ids.rosterID, WaveID: ids.waveID,
				TransitionKind: "ready_window_no_show", ReadyWindowID: nullableUUIDValue(ids.deadlineID),
				ParticipantID: nullableUUIDValue(ids.participantID), ResolvedAt: resolvedAt,
			},
		},
		{
			name: "reconnect receipt carries ready window",
			deadline: recoveryusecase.PendingDeadline{
				Kind: recoveryusecase.DeadlineKindReconnect, ID: ids.deadlineID, ExpectedRevision: 3,
				TournamentID: ids.tournamentID, RosterID: ids.rosterID, WaveID: ids.waveID,
				SeriesID: ids.seriesID, GameID: ids.gameID, PauseID: ids.pauseID, ParticipantID: ids.participantID,
			},
			receipt: sqlc.DeadlineTransitionReceipt{
				DeadlineID: ids.deadlineID, ExpectedDeadlineRevision: 3,
				TournamentID: ids.tournamentID, RosterID: ids.rosterID, WaveID: ids.waveID,
				TransitionKind: "reconnect_interval_expired", SeriesID: nullableUUIDValue(ids.seriesID),
				GameAttemptID: nullableUUIDValue(ids.gameID), PauseID: nullableUUIDValue(ids.pauseID),
				ParticipantID: nullableUUIDValue(ids.participantID), ReadyWindowID: nullableUUIDValue(ids.deadlineID),
				ResolvedAt: resolvedAt,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if recoveryReceiptMatchesDeadline(test.receipt, test.deadline) {
				t.Fatal("recoveryReceiptMatchesDeadline() accepted an unrelated receipt identity")
			}
		})
	}
}

type recoveryReceiptIDs struct {
	deadlineID    uuid.UUID
	tournamentID  uuid.UUID
	rosterID      uuid.UUID
	waveID        uuid.UUID
	seriesID      uuid.UUID
	gameID        uuid.UUID
	pauseID       uuid.UUID
	participantID uuid.UUID
}

func recoveryReceiptTestIDs() recoveryReceiptIDs {
	return recoveryReceiptIDs{
		deadlineID:    uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		tournamentID:  uuid.MustParse("00000000-0000-4000-8000-000000000002"),
		rosterID:      uuid.MustParse("00000000-0000-4000-8000-000000000003"),
		waveID:        uuid.MustParse("00000000-0000-4000-8000-000000000004"),
		seriesID:      uuid.MustParse("00000000-0000-4000-8000-000000000005"),
		gameID:        uuid.MustParse("00000000-0000-4000-8000-000000000006"),
		pauseID:       uuid.MustParse("00000000-0000-4000-8000-000000000007"),
		participantID: uuid.MustParse("00000000-0000-4000-8000-000000000008"),
	}
}
