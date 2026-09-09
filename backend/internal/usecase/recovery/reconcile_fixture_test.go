package recovery_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	recoverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery/mocks"
)

type recoveryRearmerHarness struct {
	rearmer *recoverymocks.MockRecoveryRearmer
	mu      sync.Mutex
	plans   []recovery.RecoveryRearmPlan
	err     error
}

func newRecoveryRearmerHarness(
	t *testing.T,
	err error,
) *recoveryRearmerHarness {
	t.Helper()
	harness := &recoveryRearmerHarness{err: err}
	harness.rearmer = recoverymocks.NewMockRecoveryRearmer(t)
	harness.rearmer.EXPECT().RearmRecovery(mock.Anything, mock.Anything).
		RunAndReturn(harness.apply).Maybe()
	return harness
}

func (r *recoveryRearmerHarness) apply(
	_ context.Context,
	plan recovery.RecoveryRearmPlan,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plans = append(r.plans, plan)
	return r.err
}

func (r *recoveryRearmerHarness) plansSnapshot() []recovery.RecoveryRearmPlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recovery.RecoveryRearmPlan(nil), r.plans...)
}

func newRecoveryClock(t *testing.T, now time.Time) *recoverymocks.MockClock {
	t.Helper()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Once()
	return clock
}

func recoveryFixture(
	t *testing.T,
	now time.Time,
) (recovery.RecoveryGraph, recovery.RecoveryDeadlineEvidence, recovery.RecoveryDeadlineEvidence) {
	t.Helper()
	tournamentID := recoveryID(1)
	firstParticipantID := recoveryID(2)
	secondParticipantID := recoveryID(3)
	firstPlayerID := recoveryID(4)
	secondPlayerID := recoveryID(5)
	waveID := recoveryID(10)
	windowID := recoveryID(12)
	openedAt := now.Add(-2 * time.Minute)
	consumedAt := openedAt.Add(30 * time.Second)
	startedAt := consumedAt
	seriesID := recoveryID(20)
	slotID := recoveryID(30)
	gameID := recoveryID(40)
	scoreRevisionID := domain.SeriesScoreRevisionID(recoveryID(51))

	assignment := recoveryAssignment(t, firstParticipantID, secondParticipantID, gameID)
	series := domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.SeriesFormatBO3, State: domain.SeriesStateActive,
		CurrentScoreRevisionID: &scoreRevisionID,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive,
			}},
		}},
	}
	waves := []domain.Wave{
		{
			ID: waveID, TournamentID: tournamentID,
			RevisionID: domain.WaveRevisionID(recoveryID(11)),
			State:      domain.WaveStateActive,
			Members: []domain.WaveMember{
				{ParticipantID: firstParticipantID, Ready: true},
				{ParticipantID: secondParticipantID, Ready: true},
			},
			ReadyWindow: &domain.ReadyWindow{
				ID: windowID, WaveID: waveID,
				RevisionID: domain.ReadyWindowRevisionID(recoveryID(13)),
				State:      domain.ReadyWindowStateConsumed,
				OpenedAt:   openedAt, Deadline: openedAt.Add(time.Minute), ConsumedAt: &consumedAt,
			},
			StartedAt: &startedAt,
		},
		{
			ID: recoveryID(14), TournamentID: tournamentID,
			RevisionID: domain.WaveRevisionID(recoveryID(15)),
			State:      domain.WaveStateReadyWindowOpen,
			Members: []domain.WaveMember{
				{ParticipantID: firstParticipantID},
				{ParticipantID: secondParticipantID},
			},
			ReadyWindow: &domain.ReadyWindow{
				ID: recoveryID(16), WaveID: recoveryID(14),
				RevisionID: domain.ReadyWindowRevisionID(recoveryID(17)),
				State:      domain.ReadyWindowStateOpen,
				OpenedAt:   now.Add(-time.Minute), Deadline: now.Add(4 * time.Minute),
			},
		},
	}
	derivedRevisionID := domain.DerivedRevisionID(recoveryID(60))
	projection, err := domain.NewProjectionRevision(
		derivedRevisionID,
		tournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: seriesID},
		1,
		nil,
		now.Add(-time.Minute),
		[]byte("series score 0:0"),
	)
	require.NoError(t, err)
	revisions, err := domain.NewRevisionGraph([]domain.ProjectionRevision{projection}, nil)
	require.NoError(t, err)
	cursor := recovery.RecoveryCursor{
		SchemaVersion:      recovery.RecoveryCursorSchemaVersion,
		TournamentID:       tournamentID,
		LastSequence:       1,
		ProjectionRevision: 1,
		DerivedRevisionID:  derivedRevisionID,
	}
	lease := &recovery.RecoveryLease{
		TournamentID: tournamentID, LeaseID: recoveryID(70), HolderID: recoveryID(71),
		Revision: 3, AcquiredAt: now.Add(-time.Minute), RenewedAt: now.Add(-30 * time.Second),
		ExpiresAt: now.Add(5 * time.Minute),
	}
	gameDeadline := recovery.RecoveryDeadlineEvidence{
		Work: recovery.RecoveryWork{
			Kind: recovery.RecoveryWorkGame, TournamentID: tournamentID, WaveID: waveID,
			SeriesID: seriesID, SlotID: slotID, GameID: gameID,
		},
		Deadline: now.Add(2 * time.Minute),
	}
	windowDeadline := recovery.RecoveryDeadlineEvidence{
		Work: recovery.RecoveryWork{
			Kind: recovery.RecoveryWorkReadyWindow, TournamentID: tournamentID,
			WaveID: recoveryID(14), ReadyWindowID: recoveryID(16),
		},
		Deadline: now.Add(4 * time.Minute),
	}
	graph := recovery.RecoveryGraph{
		TournamentID: tournamentID,
		Tournament:   domain.Tournament{State: domain.TournamentStateSwiss},
		Roster: domain.Roster{
			TournamentID: tournamentID, Locked: true, ExecutionStarted: true,
			Participants: []domain.Participant{
				{ID: firstParticipantID, TournamentID: tournamentID, PlayerID: firstPlayerID, Seed: 1, Attendance: domain.AttendanceStateCheckedIn},
				{ID: secondParticipantID, TournamentID: tournamentID, PlayerID: secondPlayerID, Seed: 2, Attendance: domain.AttendanceStateCheckedIn},
			},
		},
		Waves:  waves,
		Series: []recovery.RecoverySeries{{WaveID: waveID, Series: series}},
		Assignments: []recovery.RecoveryAssignment{{
			WaveID: waveID, SeriesID: seriesID, SlotID: slotID, GameID: gameID,
			Assignment: assignment,
		}},
		Scores: []recovery.RecoveryScoreEvidence{{
			SeriesID: seriesID, Score: domain.SeriesScore{},
			RevisionID: scoreRevisionID, DerivedRevisionID: derivedRevisionID,
		}},
		Revisions: revisions,
		Reservations: []domain.ParticipantReservation{
			{PlayerID: firstPlayerID, ReservationID: recoveryID(80), TournamentID: tournamentID, Revision: 1, AcquiredAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)},
			{PlayerID: secondPlayerID, ReservationID: recoveryID(81), TournamentID: tournamentID, Revision: 1, AcquiredAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)},
		},
		Lease: lease, Deadlines: []recovery.RecoveryDeadlineEvidence{windowDeadline, gameDeadline},
		Cursor: cursor, LastSequence: 1,
	}
	return graph, gameDeadline, windowDeadline
}

func recoveryAssignment(
	t *testing.T,
	firstParticipantID, secondParticipantID, attemptID uuid.UUID,
) domain.Assignment {
	t.Helper()
	snapshots := make([]domain.AssignmentTaskSnapshot, 3)
	for index := range snapshots {
		snapshots[index] = domain.AssignmentTaskSnapshot{
			SnapshotID: recoveryID(100 + index), TaskID: recoveryID(110 + index),
			Version: 1, Kind: domain.AssignmentTaskKindNormal, Title: "recovery task",
			Description: "recovery fixture", Category: domain.CategoryWeb,
			Difficulty: domain.DifficultyMedium, TimeLimit: 180, Flag: "fixture-flag",
		}
	}
	assignment, err := domain.NewAssignment(
		recoveryID(90),
		attemptID,
		firstParticipantID,
		secondParticipantID,
		snapshots[0],
		snapshots[1:],
	)
	require.NoError(t, err)
	return assignment
}

func recoveryID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(time.Unix(int64(value), 0).UTC().Format(time.RFC3339)))
}

var _ recovery.RecoveryRearmer = (*recoverymocks.MockRecoveryRearmer)(nil)
