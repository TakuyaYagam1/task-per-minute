package recovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaRecoveryReconciler(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	t.Run("validates the full graph before one deterministic rearm", func(t *testing.T) {
		graph, gameDeadline, windowDeadline := arenaRecoveryFixture(t, now)
		rearmer := &arenaRecoveryRearmerFake{}

		result, err := NewArenaRecoveryReconciler(rearmer, recoveryClock{now: now}).Reconcile(
			t.Context(),
			graph,
		)

		require.NoError(t, err)
		wantPlan := ArenaRecoveryRearmPlan{
			TournamentID: graph.TournamentID,
			Cursor:       graph.Cursor,
			Lease:        *graph.Lease,
			Work:         []ArenaRecoveryDeadlineEvidence{gameDeadline, windowDeadline},
		}
		require.Equal(t, ArenaRecoveryResult{Plan: wantPlan, Rearmed: 2}, result)
		require.Equal(t, []ArenaRecoveryRearmPlan{wantPlan}, rearmer.plansSnapshot())
	})

	t.Run("requires reservations only for checked-in roster members", func(t *testing.T) {
		graph, _, _ := arenaRecoveryFixture(t, now)
		graph.Roster.Participants = append(graph.Roster.Participants, domain.ArenaParticipant{
			ID: arenaRecoveryID(6), TournamentID: graph.TournamentID,
			PlayerID: arenaRecoveryID(7), Seed: 3,
			Attendance: domain.ArenaAttendanceStateWithdrawn,
		})
		rearmer := &arenaRecoveryRearmerFake{}

		result, err := NewArenaRecoveryReconciler(rearmer, recoveryClock{now: now}).Reconcile(
			t.Context(), graph,
		)

		require.NoError(t, err)
		require.Empty(t, result.FailReason)
		require.Equal(t, 2, result.Rearmed)
		require.Len(t, rearmer.plansSnapshot(), 1)
	})

	t.Run("routes missing durable evidence to an exact fail-closed reason", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*ArenaRecoveryGraph)
			want   ArenaRecoveryFailReason
		}{
			{
				name: "result",
				mutate: func(graph *ArenaRecoveryGraph) {
					game := &graph.Series[0].Series.Slots[0].Attempts[0]
					winnerID := graph.Series[0].Series.FirstParticipantID
					revisionID := domain.ArenaOfficialResultRevisionID(arenaRecoveryID(95))
					game.State = domain.ArenaGameStateCompleted
					game.ResultReason = domain.ArenaGameResultReasonSolved
					game.WinnerID = &winnerID
					game.ResultRevisionID = &revisionID
				},
				want: ArenaRecoveryFailReasonMissingResult,
			},
			{
				name: "score",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Scores = nil
				},
				want: ArenaRecoveryFailReasonMissingScore,
			},
			{
				name: "pause",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Series[0].Series.Slots[0].Attempts[0].State = domain.ArenaGameStatePaused
				},
				want: ArenaRecoveryFailReasonMissingPause,
			},
			{
				name: "revision",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Scores[0].RevisionID = domain.ArenaSeriesScoreRevisionID{}
				},
				want: ArenaRecoveryFailReasonMissingRevision,
			},
			{
				name: "reservation",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Reservations = graph.Reservations[:1]
				},
				want: ArenaRecoveryFailReasonMissingReservation,
			},
			{
				name: "lease",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Lease = nil
				},
				want: ArenaRecoveryFailReasonMissingLease,
			},
			{
				name: "deadline",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Deadlines = graph.Deadlines[:1]
				},
				want: ArenaRecoveryFailReasonMissingDeadline,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				graph, _, _ := arenaRecoveryFixture(t, now)
				test.mutate(&graph)
				rearmer := &arenaRecoveryRearmerFake{}

				result, err := NewArenaRecoveryReconciler(
					rearmer,
					recoveryClock{now: now},
				).Reconcile(t.Context(), graph)

				require.NoError(t, err)
				require.Equal(t, ArenaRecoveryResult{FailReason: test.want}, result)
				require.Empty(t, rearmer.plansSnapshot())
			})
		}
	})

	t.Run("rejects invalid child graph DAG and cursor before rearming", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*ArenaRecoveryGraph)
			want   ArenaRecoveryFailReason
		}{
			{
				name: "child graph",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Waves[0].Members[0].ParticipantID = arenaRecoveryID(96)
				},
				want: ArenaRecoveryFailReasonInvalidGraph,
			},
			{
				name: "assignment attempt",
				mutate: func(graph *ArenaRecoveryGraph) {
					series := graph.Series[0].Series
					graph.Assignments[0].Assignment = arenaRecoveryAssignment(
						t, series.FirstParticipantID, series.SecondParticipantID, arenaRecoveryID(97),
					)
				},
				want: ArenaRecoveryFailReasonInvalidGraph,
			},
			{
				name: "DAG",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Revisions = domain.ArenaRevisionGraph{}
				},
				want: ArenaRecoveryFailReasonInvalidDAG,
			},
			{
				name: "cursor",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Cursor.LastSequence++
				},
				want: ArenaRecoveryFailReasonInvalidCursor,
			},
			{
				name: "realtime cursor projection",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Cursor.ProjectionRevision++
				},
				want: ArenaRecoveryFailReasonInvalidCursor,
			},
			{
				name: "derived cursor revision",
				mutate: func(graph *ArenaRecoveryGraph) {
					graph.Cursor.DerivedRevisionID = domain.ArenaDerivedRevisionID(arenaRecoveryID(98))
				},
				want: ArenaRecoveryFailReasonInvalidCursor,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				graph, _, _ := arenaRecoveryFixture(t, now)
				test.mutate(&graph)
				rearmer := &arenaRecoveryRearmerFake{}

				result, err := NewArenaRecoveryReconciler(
					rearmer,
					recoveryClock{now: now},
				).Reconcile(t.Context(), graph)

				require.NoError(t, err)
				require.Equal(t, ArenaRecoveryResult{FailReason: test.want}, result)
				require.Empty(t, rearmer.plansSnapshot())
			})
		}
	})

	t.Run("returns an empty plan when no work needs rearming", func(t *testing.T) {
		graph, _, _ := arenaRecoveryFixture(t, now)
		graph.Series[0].Series.Slots[0].Attempts[0].State = domain.ArenaGameStatePlanned
		graph.Waves[1].State = domain.ArenaWaveStatePlanned
		graph.Waves[1].ReadyWindow = nil
		graph.Deadlines = nil
		graph.Lease = nil
		rearmer := &arenaRecoveryRearmerFake{}

		result, err := NewArenaRecoveryReconciler(rearmer, recoveryClock{now: now}).Reconcile(
			t.Context(),
			graph,
		)

		require.NoError(t, err)
		require.Equal(t, ArenaRecoveryResult{Plan: ArenaRecoveryRearmPlan{
			TournamentID: graph.TournamentID,
			Cursor:       graph.Cursor,
			Work:         []ArenaRecoveryDeadlineEvidence{},
		}}, result)
		require.Empty(t, rearmer.plansSnapshot())
	})

	t.Run("preserves a rearmer failure", func(t *testing.T) {
		graph, _, _ := arenaRecoveryFixture(t, now)
		wantErr := errors.New("timer store unavailable")
		rearmer := &arenaRecoveryRearmerFake{err: wantErr}

		result, err := NewArenaRecoveryReconciler(rearmer, recoveryClock{now: now}).Reconcile(
			t.Context(),
			graph,
		)

		require.Zero(t, result)
		require.ErrorIs(t, err, wantErr)
		require.Len(t, rearmer.plansSnapshot(), 1)
	})
}

type arenaRecoveryRearmerFake struct {
	mu    sync.Mutex
	plans []ArenaRecoveryRearmPlan
	err   error
}

func (r *arenaRecoveryRearmerFake) RearmArenaRecovery(
	_ context.Context,
	plan ArenaRecoveryRearmPlan,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plans = append(r.plans, plan)
	return r.err
}

func (r *arenaRecoveryRearmerFake) plansSnapshot() []ArenaRecoveryRearmPlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ArenaRecoveryRearmPlan(nil), r.plans...)
}

func arenaRecoveryFixture(
	t *testing.T,
	now time.Time,
) (ArenaRecoveryGraph, ArenaRecoveryDeadlineEvidence, ArenaRecoveryDeadlineEvidence) {
	t.Helper()
	tournamentID := arenaRecoveryID(1)
	firstParticipantID := arenaRecoveryID(2)
	secondParticipantID := arenaRecoveryID(3)
	firstPlayerID := arenaRecoveryID(4)
	secondPlayerID := arenaRecoveryID(5)
	waveID := arenaRecoveryID(10)
	windowID := arenaRecoveryID(12)
	openedAt := now.Add(-2 * time.Minute)
	consumedAt := openedAt.Add(30 * time.Second)
	startedAt := consumedAt
	seriesID := arenaRecoveryID(20)
	slotID := arenaRecoveryID(30)
	gameID := arenaRecoveryID(40)
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(arenaRecoveryID(51))

	assignment := arenaRecoveryAssignment(t, firstParticipantID, secondParticipantID, gameID)
	series := domain.ArenaSeries{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.ArenaSeriesFormatBO3, State: domain.ArenaSeriesStateActive,
		CurrentScoreRevisionID: &scoreRevisionID,
		Slots: []domain.ArenaGameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.ArenaGameStateActive,
			}},
		}},
	}
	waves := []domain.ArenaWave{
		{
			ID: waveID, TournamentID: tournamentID,
			RevisionID: domain.ArenaWaveRevisionID(arenaRecoveryID(11)),
			State:      domain.ArenaWaveStateActive,
			Members: []domain.ArenaWaveMember{
				{ParticipantID: firstParticipantID, Ready: true},
				{ParticipantID: secondParticipantID, Ready: true},
			},
			ReadyWindow: &domain.ArenaReadyWindow{
				ID: windowID, WaveID: waveID,
				RevisionID: domain.ArenaReadyWindowRevisionID(arenaRecoveryID(13)),
				State:      domain.ArenaReadyWindowStateConsumed,
				OpenedAt:   openedAt, Deadline: openedAt.Add(time.Minute), ConsumedAt: &consumedAt,
			},
			StartedAt: &startedAt,
		},
		{
			ID: arenaRecoveryID(14), TournamentID: tournamentID,
			RevisionID: domain.ArenaWaveRevisionID(arenaRecoveryID(15)),
			State:      domain.ArenaWaveStateReadyWindowOpen,
			Members: []domain.ArenaWaveMember{
				{ParticipantID: firstParticipantID},
				{ParticipantID: secondParticipantID},
			},
			ReadyWindow: &domain.ArenaReadyWindow{
				ID: arenaRecoveryID(16), WaveID: arenaRecoveryID(14),
				RevisionID: domain.ArenaReadyWindowRevisionID(arenaRecoveryID(17)),
				State:      domain.ArenaReadyWindowStateOpen,
				OpenedAt:   now.Add(-time.Minute), Deadline: now.Add(4 * time.Minute),
			},
		},
	}
	derivedRevisionID := domain.ArenaDerivedRevisionID(arenaRecoveryID(60))
	projection, err := domain.NewArenaProjectionRevision(
		derivedRevisionID,
		tournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesScore, EntityID: seriesID},
		1,
		nil,
		now.Add(-time.Minute),
		[]byte("series score 0:0"),
	)
	require.NoError(t, err)
	revisions, err := domain.NewArenaRevisionGraph([]domain.ArenaProjectionRevision{projection}, nil)
	require.NoError(t, err)
	cursor := ArenaRecoveryCursor{
		SchemaVersion:      ArenaRecoveryCursorSchemaVersion,
		TournamentID:       tournamentID,
		LastSequence:       1,
		ProjectionRevision: 1,
		DerivedRevisionID:  derivedRevisionID,
	}
	lease := &ArenaRecoveryLease{
		TournamentID: tournamentID, LeaseID: arenaRecoveryID(70), HolderID: arenaRecoveryID(71),
		Revision: 3, AcquiredAt: now.Add(-time.Minute), RenewedAt: now.Add(-30 * time.Second),
		ExpiresAt: now.Add(5 * time.Minute),
	}
	gameDeadline := ArenaRecoveryDeadlineEvidence{
		Work: ArenaRecoveryWork{
			Kind: ArenaRecoveryWorkGame, TournamentID: tournamentID, WaveID: waveID,
			SeriesID: seriesID, SlotID: slotID, GameID: gameID,
		},
		Deadline: now.Add(2 * time.Minute),
	}
	windowDeadline := ArenaRecoveryDeadlineEvidence{
		Work: ArenaRecoveryWork{
			Kind: ArenaRecoveryWorkReadyWindow, TournamentID: tournamentID,
			WaveID: arenaRecoveryID(14), ReadyWindowID: arenaRecoveryID(16),
		},
		Deadline: now.Add(4 * time.Minute),
	}
	graph := ArenaRecoveryGraph{
		TournamentID: tournamentID,
		Tournament:   domain.ArenaTournament{State: domain.ArenaTournamentStateSwiss},
		Roster: domain.ArenaRoster{
			TournamentID: tournamentID, Locked: true, ExecutionStarted: true,
			Participants: []domain.ArenaParticipant{
				{ID: firstParticipantID, TournamentID: tournamentID, PlayerID: firstPlayerID, Seed: 1, Attendance: domain.ArenaAttendanceStateCheckedIn},
				{ID: secondParticipantID, TournamentID: tournamentID, PlayerID: secondPlayerID, Seed: 2, Attendance: domain.ArenaAttendanceStateCheckedIn},
			},
		},
		Waves:  waves,
		Series: []ArenaRecoverySeries{{WaveID: waveID, Series: series}},
		Assignments: []ArenaRecoveryAssignment{{
			WaveID: waveID, SeriesID: seriesID, SlotID: slotID, GameID: gameID,
			Assignment: assignment,
		}},
		Scores: []ArenaRecoveryScoreEvidence{{
			SeriesID: seriesID, Score: domain.ArenaSeriesScore{},
			RevisionID: scoreRevisionID, DerivedRevisionID: derivedRevisionID,
		}},
		Revisions: revisions,
		Reservations: []domain.ParticipantReservation{
			{PlayerID: firstPlayerID, ReservationID: arenaRecoveryID(80), OwnerKind: domain.ParticipantReservationOwnerArena, OwnerID: tournamentID, Revision: 1, AcquiredAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)},
			{PlayerID: secondPlayerID, ReservationID: arenaRecoveryID(81), OwnerKind: domain.ParticipantReservationOwnerArena, OwnerID: tournamentID, Revision: 1, AcquiredAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)},
		},
		Lease: lease, Deadlines: []ArenaRecoveryDeadlineEvidence{windowDeadline, gameDeadline},
		Cursor: cursor, LastSequence: 1,
	}
	return graph, gameDeadline, windowDeadline
}

func arenaRecoveryAssignment(
	t *testing.T,
	firstParticipantID, secondParticipantID, attemptID uuid.UUID,
) domain.ArenaAssignment {
	t.Helper()
	snapshots := make([]domain.ArenaTaskSnapshot, 3)
	for index := range snapshots {
		snapshots[index] = domain.ArenaTaskSnapshot{
			SnapshotID: arenaRecoveryID(100 + index), TaskID: arenaRecoveryID(110 + index),
			Version: 1, Kind: domain.ArenaTaskKindNormal, Title: "recovery task",
			Description: "recovery fixture", Category: domain.CategoryWeb,
			Difficulty: domain.DifficultyMedium, TimeLimit: 180, Flag: "fixture-flag",
		}
	}
	assignment, err := domain.NewArenaAssignment(
		arenaRecoveryID(90),
		attemptID,
		firstParticipantID,
		secondParticipantID,
		snapshots[0],
		snapshots[1:],
	)
	require.NoError(t, err)
	return assignment
}

func arenaRecoveryID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(time.Unix(int64(value), 0).UTC().Format(time.RFC3339)))
}

var _ ArenaRecoveryRearmer = (*arenaRecoveryRearmerFake)(nil)
