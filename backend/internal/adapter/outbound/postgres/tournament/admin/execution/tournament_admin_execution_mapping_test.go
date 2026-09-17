package execution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
)

func TestTournamentAdminStandingsMapsExactPublishedRoster(t *testing.T) {
	t.Parallel()

	participants := []pairingusecase.PairingParticipant{
		{ID: tournamentExecutionID(1), StableSeed: 10},
		{ID: tournamentExecutionID(2), StableSeed: 20},
	}
	acceptedSolveTime := int64(2 * time.Second)
	document, err := json.Marshal(tournamentAdminStandingsDocument{Entries: []tournamentAdminStanding{
		{
			ParticipantID: participants[1].ID, Position: 2, Points: 3, Buchholz: 4,
			EffectiveTimeNS: int64(7 * time.Second),
		},
		{
			ParticipantID: participants[0].ID, Position: 1, Points: 6, Buchholz: 8,
			HeadToHeadPoints: 3, HeadToHeadApplied: true,
			EffectiveTimeNS: int64(5 * time.Second), AcceptedSolveTimeNS: &acceptedSolveTime,
		},
	}})
	require.NoError(t, err)

	standings, err := tournamentAdminStandings(document, participants)
	require.NoError(t, err)
	require.Len(t, standings, 2)
	require.Equal(t, participants[0].ID, standings[0].ParticipantID)
	require.Equal(t, int64(5000), standings[0].EffectiveTimeMS)
	require.NotNil(t, standings[0].AcceptedSolveTimeMS)
	require.Equal(t, int64(2000), *standings[0].AcceptedSolveTimeMS)
	require.Equal(t, participants[1].ID, standings[1].ParticipantID)
}

func TestTournamentAdminStandingsSeedsInitialRoster(t *testing.T) {
	t.Parallel()

	participants := []pairingusecase.PairingParticipant{
		{ID: tournamentExecutionID(2), StableSeed: 20},
		{ID: tournamentExecutionID(1), StableSeed: 10},
	}
	document, err := json.Marshal(tournamentAdminStandingsDocument{Entries: []tournamentAdminStanding{}})
	require.NoError(t, err)

	standings, err := tournamentAdminStandings(document, participants)
	require.NoError(t, err)
	require.Equal(t, []pairingusecase.SwissStandingView{
		{
			ParticipantID: participants[1].ID, Position: 1, PointsLabel: "provisional",
			BuchholzStatus: "provisional", StableSeed: participants[1].StableSeed,
		},
		{
			ParticipantID: participants[0].ID, Position: 2, PointsLabel: "provisional",
			BuchholzStatus: "provisional", StableSeed: participants[0].StableSeed,
		},
	}, standings)
}

func TestTournamentAdminStandingsRejectsMissingEntries(t *testing.T) {
	t.Parallel()

	_, err := tournamentAdminStandings(
		[]byte(`{}`),
		[]pairingusecase.PairingParticipant{{ID: tournamentExecutionID(1), StableSeed: 1}},
	)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentAdminStandingsRejectsForeignParticipant(t *testing.T) {
	t.Parallel()

	participants := []pairingusecase.PairingParticipant{{ID: tournamentExecutionID(1), StableSeed: 1}}
	document, err := json.Marshal(tournamentAdminStandingsDocument{Entries: []tournamentAdminStanding{{
		ParticipantID: tournamentExecutionID(2), Position: 1,
	}}})
	require.NoError(t, err)

	_, err = tournamentAdminStandings(document, participants)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentAdminCompletedRoundCountAllowsUnstartedCurrentRound(t *testing.T) {
	t.Parallel()

	roundID := tournamentExecutionID(70)
	completedRoundID := tournamentExecutionID(71)
	completedAt := time.Date(2026, time.September, 6, 12, 1, 0, 0, time.UTC)
	startedRound := sqlc.LockTournamentPairingRoundsRow{
		ID: completedRoundID, RoundNumber: 1, Revision: 2,
		LockedAt: pgtype.Timestamptz{Time: completedAt, Valid: true},
	}
	currentRound := sqlc.LockTournamentPairingRoundsRow{
		ID: roundID, RoundNumber: 2, Revision: 1,
	}

	completed, err := tournamentAdminCompletedRoundCount(
		[]sqlc.LockTournamentPairingRoundsRow{startedRound, currentRound},
		[]sqlc.LockTournamentPairingWavesRow{
			{RoundID: completedRoundID, WaveID: tournamentExecutionID(72), State: string(domain.WaveStateCompleted)},
			{RoundID: roundID, WaveID: tournamentExecutionID(73), State: string(domain.WaveStatePlanned)},
		},
	)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
}

func TestTournamentAdminWaveViewKeepsByeOutsideSeries(t *testing.T) {
	t.Parallel()

	header, members, series, byeID := tournamentExecutionWaveRows()

	view, playable, err := tournamentAdminWaveView(header, members, series)
	require.NoError(t, err)
	require.NotNil(t, view.ByeParticipantID)
	require.Equal(t, byeID, *view.ByeParticipantID)
	require.Len(t, view.Wave.Members, 5)
	require.Len(t, view.SeriesIDs, 4)
	require.Len(t, playable, 4)
	_, hasSyntheticSeries := view.SeriesIDs[byeID]
	require.False(t, hasSyntheticSeries)
}

func TestTournamentAdminWaveViewRejectsPairedBye(t *testing.T) {
	t.Parallel()

	header, members, series, byeID := tournamentExecutionWaveRows()
	series[1].SecondParticipantID = byeID

	_, _, err := tournamentAdminWaveView(header, members, series)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentAdminWaveGraphRejectsCrossSeriesDelivery(t *testing.T) {
	t.Parallel()

	_, _, series, _ := tournamentExecutionWaveRows()
	firstGameID := tournamentExecutionID(41)
	secondGameID := tournamentExecutionID(42)
	games := []sqlc.LockTournamentAdminWaveGamesRow{
		{SeriesID: series[0].ID, GameID: firstGameID, State: string(domain.GameStateReady), Revision: 1},
		{SeriesID: series[1].ID, GameID: secondGameID, State: string(domain.GameStateReady), Revision: 1},
	}
	playable := map[uuid.UUID]struct{}{
		series[0].FirstParticipantID: {}, series[0].SecondParticipantID: {},
		series[1].FirstParticipantID: {}, series[1].SecondParticipantID: {},
	}
	deliveries := []sqlc.LockTournamentAdminWaveDeliveriesRow{{
		ID: tournamentExecutionID(50), AttemptID: firstGameID,
		ParticipantID: series[1].FirstParticipantID,
	}}

	_, err := tournamentAdminWaveGraph(series, games, nil, deliveries, playable)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentAdminWaveGraphCountsCompleteStartEvidence(t *testing.T) {
	t.Parallel()

	_, _, series, _ := tournamentExecutionWaveRows()
	firstGameID := tournamentExecutionID(41)
	secondGameID := tournamentExecutionID(42)
	games := []sqlc.LockTournamentAdminWaveGamesRow{
		{SeriesID: series[0].ID, GameID: firstGameID, State: string(domain.GameStateReady), Revision: 1},
		{SeriesID: series[1].ID, GameID: secondGameID, State: string(domain.GameStateReady), Revision: 1},
	}
	assignments := []sqlc.LockTournamentAdminWaveAssignmentsRow{
		{ID: tournamentExecutionID(51), AttemptID: firstGameID, Revision: 1},
		{ID: tournamentExecutionID(52), AttemptID: secondGameID, Revision: 1},
	}
	deliveries := []sqlc.LockTournamentAdminWaveDeliveriesRow{
		{ID: tournamentExecutionID(61), AttemptID: firstGameID, ParticipantID: series[0].FirstParticipantID},
		{ID: tournamentExecutionID(62), AttemptID: firstGameID, ParticipantID: series[0].SecondParticipantID},
		{ID: tournamentExecutionID(63), AttemptID: secondGameID, ParticipantID: series[1].FirstParticipantID},
		{ID: tournamentExecutionID(64), AttemptID: secondGameID, ParticipantID: series[1].SecondParticipantID},
	}
	playable := map[uuid.UUID]struct{}{
		series[0].FirstParticipantID: {}, series[0].SecondParticipantID: {},
		series[1].FirstParticipantID: {}, series[1].SecondParticipantID: {},
	}

	graph, err := tournamentAdminWaveGraph(series, games, assignments, deliveries, playable)
	require.NoError(t, err)
	require.Equal(t, tournamentadmin.WaveGraph{
		SeriesCount: 2, PlayableMemberCount: 4, ReadySeriesCount: 2,
		CurrentGameCount: 2, ReadyGameCount: 2, AssignmentCount: 2, DeliveryMemberCount: 4,
	}, graph)
}

func tournamentExecutionWaveRows() (
	sqlc.LockTournamentAdminWaveAuthorityRow,
	[]sqlc.LockTournamentAdminWaveMembersRow,
	[]sqlc.LockTournamentAdminWaveSeriesRow,
	uuid.UUID,
) {
	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	participants := []uuid.UUID{
		tournamentExecutionID(11), tournamentExecutionID(12), tournamentExecutionID(13),
		tournamentExecutionID(14), tournamentExecutionID(15),
	}
	members := make([]sqlc.LockTournamentAdminWaveMembersRow, len(participants))
	for index, participantID := range participants {
		members[index] = sqlc.LockTournamentAdminWaveMembersRow{
			ParticipantID: participantID, ReadinessRevision: 1,
		}
	}
	header := sqlc.LockTournamentAdminWaveAuthorityRow{
		ID: tournamentExecutionID(20), TournamentID: tournamentExecutionID(21),
		RevisionID: tournamentExecutionID(22), Revision: 1, State: string(domain.WaveStatePlanned),
		CreatedAt:        pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:        pgtype.Timestamptz{Time: now, Valid: true},
		ByeParticipantID: uuid.NullUUID{UUID: participants[4], Valid: true},
	}
	series := []sqlc.LockTournamentAdminWaveSeriesRow{
		{
			ID: tournamentExecutionID(31), FirstParticipantID: participants[0],
			SecondParticipantID: participants[1], Format: string(domain.SeriesFormatBO1),
			State: string(domain.SeriesStateReady), Revision: 1,
		},
		{
			ID: tournamentExecutionID(32), FirstParticipantID: participants[2],
			SecondParticipantID: participants[3], Format: string(domain.SeriesFormatBO1),
			State: string(domain.SeriesStateReady), Revision: 1,
		},
	}
	return header, members, series, participants[4]
}

func tournamentExecutionID(value byte) uuid.UUID {
	var id uuid.UUID
	id[len(id)-1] = value
	return id
}
