package recovery

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaGoldenRecovery(t *testing.T) {
	t.Parallel()

	t.Run("reconstructs durable Golden state", func(t *testing.T) {
		t.Parallel()

		input := goldenRecoveryFixture()
		result, err := RecoverGoldenGroup(input)
		require.NoError(t, err)

		snapshot := result.Group.Snapshot()
		require.Equal(t, input.Group.ID, snapshot.ID)
		require.Equal(t, input.Group.RevisionID, snapshot.RevisionID)
		require.Len(t, snapshot.Attempts, 3)
		require.Equal(t, input.Attempts[1].Attempt.ID, snapshot.Attempts[1].ID)
		require.Equal(t, []uuid.UUID{input.Attempts[1].Attempt.ID}, result.RetainedPreStartAttemptIDs)
		require.Equal(t, []uuid.UUID{goldenRecoveryID(3)}, result.PermanentExclusions)

		require.Len(t, result.CommittedPriorAttempts, 1)
		require.Equal(t, 1, result.CommittedPriorAttempts[0].AttemptNo)
		require.Equal(t, []GoldenRecoveryPositionCommit{
			input.Attempts[0].PositionCommits[1],
			input.Attempts[0].PositionCommits[0],
		}, result.CommittedPriorAttempts[0].Positions)

		require.NotNil(t, result.LiveAttempt)
		require.Equal(t, input.Attempts[2].Attempt.ID, result.LiveAttempt.AttemptID)
		require.Equal(t, []GoldenRecoveryReserve{{
			MembershipID:  goldenRecoveryID(43),
			ParticipantID: goldenRecoveryID(4),
			Position:      1,
			PromotedAt:    input.Attempts[2].Memberships[2].PromotedAt,
		}}, result.LiveAttempt.Reserves)
		require.Equal(t, []uuid.UUID{goldenRecoveryID(1), goldenRecoveryID(2), goldenRecoveryID(4)}, result.LiveAttempt.ReadyParticipantIDs)
		require.Equal(t, []GoldenRecoveryProvisional{
			{
				SubmissionID: goldenRecoveryID(71), ParticipantID: goldenRecoveryID(2),
				ServerSequence: 1, Position: 2,
			},
			{
				SubmissionID: goldenRecoveryID(72), ParticipantID: goldenRecoveryID(1),
				ServerSequence: 2, Position: 1,
			},
		}, result.LiveAttempt.ProvisionalOrder)
		require.Equal(t, *input.Attempts[2].ReadyDeadline, result.LiveAttempt.Deadlines.Ready)
		require.Equal(t, input.Attempts[2].ExecutionDeadline, result.LiveAttempt.Deadlines.Execution)
	})

	t.Run("fails closed when more than one attempt is live", func(t *testing.T) {
		t.Parallel()

		input := goldenRecoveryFixture()
		input.Attempts[1].Attempt.State = domain.ArenaGoldenAttemptStateWaitingReady
		input.Attempts[1].Attempt.StartedAt = nil
		input.Attempts[1].Attempt.FinishedAt = nil

		result, err := RecoverGoldenGroup(input)
		require.Zero(t, result)
		require.ErrorIs(t, err, ErrAmbiguousGoldenRecovery)
	})

	t.Run("fails closed when active readiness postdates start", func(t *testing.T) {
		t.Parallel()

		input := goldenRecoveryFixture()
		late := input.Attempts[2].Attempt.StartedAt.Add(time.Minute)
		input.Attempts[2].Memberships[0].ReadyAt = &late
		input.Attempts[2].Memberships[0].ParticipationEstablishedAt = &late

		result, err := RecoverGoldenGroup(input)
		require.Zero(t, result)
		require.ErrorIs(t, err, ErrInvalidGoldenRecovery)
	})
}

func goldenRecoveryFixture() GoldenRecoveryInput {
	selectedAt := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	firstStartedAt := selectedAt.Add(time.Minute)
	firstFinishedAt := firstStartedAt.Add(5 * time.Minute)
	retainedAt := firstFinishedAt.Add(time.Minute)
	secondStartedAt := retainedAt.Add(time.Minute)
	secondFinishedAt := secondStartedAt.Add(2 * time.Minute)
	thirdReadyAt := secondFinishedAt.Add(time.Minute)
	thirdStartedAt := thirdReadyAt.Add(time.Minute)
	readyDeadline := thirdReadyAt.Add(30 * time.Second)
	executionDeadline := thirdStartedAt.Add(10 * time.Minute)
	firstAttemptID := goldenRecoveryID(11)
	secondAttemptID := goldenRecoveryID(12)
	thirdAttemptID := goldenRecoveryID(13)
	groupID := goldenRecoveryID(20)
	groupRevisionID := domain.ArenaDerivedRevisionID(goldenRecoveryID(21))
	participants := []uuid.UUID{
		goldenRecoveryID(1), goldenRecoveryID(2), goldenRecoveryID(3), goldenRecoveryID(4),
	}

	firstSubmissions := []GoldenRecoverySubmission{
		{
			ID: goldenRecoveryID(61), MembershipID: goldenRecoveryID(30),
			ParticipantID: participants[0], ServerSequence: 1, Position: 1,
			AcceptedAt: firstStartedAt.Add(time.Minute),
		},
		{
			ID: goldenRecoveryID(62), MembershipID: goldenRecoveryID(31),
			ParticipantID: participants[1], ServerSequence: 2, Position: 2,
			AcceptedAt: firstStartedAt.Add(2 * time.Minute),
		},
	}
	firstCommits := []GoldenRecoveryPositionCommit{
		{
			ID: goldenRecoveryID(82), AttemptID: firstAttemptID,
			SubmissionID: firstSubmissions[1].ID, ParticipantID: participants[1],
			Position: 2, CommittedAt: firstSubmissions[1].AcceptedAt,
		},
		{
			ID: goldenRecoveryID(81), AttemptID: firstAttemptID,
			SubmissionID: firstSubmissions[0].ID, ParticipantID: participants[0],
			Position: 1, CommittedAt: firstSubmissions[0].AcceptedAt,
		},
	}
	secondMemberships := goldenRecoveryMemberships(35, participants, retainedAt, secondStartedAt)
	secondMemberships[2].Selection = GoldenRecoverySelectionExcluded
	secondMemberships[2].ReadyAt = nil
	secondMemberships[2].ParticipationEstablishedAt = nil
	secondMemberships[2].ExcludedAt = &retainedAt

	return GoldenRecoveryInput{
		Group: GoldenRecoveryGroup{
			ID: groupID, TournamentID: goldenRecoveryID(22), RevisionID: groupRevisionID,
			SourceProjectionRevisionID: domain.ArenaDerivedRevisionID(goldenRecoveryID(23)),
			PositionFrom:               1, PositionTo: 4, ParticipantIDs: participants,
		},
		Attempts: []GoldenRecoveryAttempt{
			{
				Attempt: domain.ArenaGoldenAttempt{
					ID: firstAttemptID, GroupID: groupID, GroupRevisionID: groupRevisionID,
					AttemptNo: 1, State: domain.ArenaGoldenAttemptStateVoid,
					ParticipantIDs: participants, StartedAt: &firstStartedAt, FinishedAt: &firstFinishedAt,
				},
				Memberships: goldenRecoveryMemberships(30, participants, selectedAt, firstStartedAt),
				Submissions: firstSubmissions, PositionCommits: firstCommits,
			},
			{
				Attempt: domain.ArenaGoldenAttempt{
					ID: secondAttemptID, GroupID: groupID, GroupRevisionID: groupRevisionID,
					AttemptNo: 2, PreviousAttemptID: &firstAttemptID,
					State:          domain.ArenaGoldenAttemptStateVoid,
					ParticipantIDs: []uuid.UUID{participants[0], participants[1], participants[3]},
					RetainedAt:     &retainedAt, StartedAt: &secondStartedAt, FinishedAt: &secondFinishedAt,
				},
				Memberships: secondMemberships,
			},
			{
				Attempt: domain.ArenaGoldenAttempt{
					ID: thirdAttemptID, GroupID: groupID, GroupRevisionID: groupRevisionID,
					AttemptNo: 3, PreviousAttemptID: &secondAttemptID,
					State:          domain.ArenaGoldenAttemptStateActive,
					ParticipantIDs: []uuid.UUID{participants[0], participants[1], participants[3]},
					StartedAt:      &thirdStartedAt,
				},
				Memberships: []GoldenRecoveryMembership{
					{
						ID: goldenRecoveryID(41), ParticipantID: participants[0],
						Selection: GoldenRecoverySelectionDirect, SelectedAt: secondFinishedAt,
						ReadyAt: &thirdReadyAt, ParticipationEstablishedAt: &thirdReadyAt,
					},
					{
						ID: goldenRecoveryID(42), ParticipantID: participants[1],
						Selection: GoldenRecoverySelectionDirect, SelectedAt: secondFinishedAt,
						ReadyAt: &thirdReadyAt, ParticipationEstablishedAt: &thirdReadyAt,
					},
					{
						ID: goldenRecoveryID(43), ParticipantID: participants[3],
						Selection: GoldenRecoverySelectionReserve, ReservePosition: 1,
						SelectedAt: secondFinishedAt, ReadyAt: &thirdReadyAt,
						ParticipationEstablishedAt: &thirdReadyAt, PromotedAt: &thirdReadyAt,
					},
					{
						ID: goldenRecoveryID(44), ParticipantID: participants[2],
						Selection: GoldenRecoverySelectionExcluded, SelectedAt: secondFinishedAt,
						ExcludedAt: &secondFinishedAt,
					},
				},
				Submissions: []GoldenRecoverySubmission{
					{
						ID: goldenRecoveryID(72), MembershipID: goldenRecoveryID(41),
						ParticipantID: participants[0], ServerSequence: 2, Position: 1,
						AcceptedAt: thirdStartedAt.Add(2 * time.Minute),
					},
					{
						ID: goldenRecoveryID(71), MembershipID: goldenRecoveryID(42),
						ParticipantID: participants[1], ServerSequence: 1, Position: 2,
						AcceptedAt: thirdStartedAt.Add(time.Minute),
					},
				},
				ReadyDeadline: &readyDeadline, ExecutionDeadline: &executionDeadline,
			},
		},
	}
}

func goldenRecoveryMemberships(
	start int,
	participants []uuid.UUID,
	selectedAt time.Time,
	readyAt time.Time,
) []GoldenRecoveryMembership {
	memberships := make([]GoldenRecoveryMembership, len(participants))
	for i, participantID := range participants {
		memberships[i] = GoldenRecoveryMembership{
			ID: goldenRecoveryID(start + i), ParticipantID: participantID,
			Selection: GoldenRecoverySelectionDirect, SelectedAt: selectedAt,
			ReadyAt: &readyAt, ParticipationEstablishedAt: &readyAt,
		}
	}
	return memberships
}

func goldenRecoveryID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-0000-0000-" + fmtID(value))
}

func fmtID(value int) string {
	const digits = "0123456789"
	result := []byte("000000000000")
	for i := len(result) - 1; value > 0; i-- {
		result[i] = digits[value%10]
		value /= 10
	}
	return string(result)
}
