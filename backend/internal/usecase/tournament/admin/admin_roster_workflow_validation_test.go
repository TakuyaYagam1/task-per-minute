package admin

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestRosterRequestDigestCanonicalizesParticipantOrder(t *testing.T) {
	t.Parallel()

	first := validRosterWorkflowReplaceCommand()
	second := first
	second.Participants = []RosterParticipantInput{
		first.Participants[3], first.Participants[1], first.Participants[0], first.Participants[2],
	}

	firstDigest, err := rosterRequestDigest(RosterOperationReplace, first)
	require.NoError(t, err)
	secondDigest, err := rosterRequestDigest(RosterOperationReplace, second)
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
}

func TestPreflightAuthorizesLockFailsClosedForStructurallyValidFailedReport(t *testing.T) {
	t.Parallel()

	tournamentID := rosterWorkflowID(1)
	rosterID := rosterWorkflowID(2)
	projectionID := rosterWorkflowID(3)
	reportID := rosterWorkflowID(4)
	playerIDs := []uuid.UUID{
		rosterWorkflowID(10), rosterWorkflowID(11), rosterWorkflowID(12), rosterWorkflowID(13),
	}
	evaluatedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	participants := make([]tournamentpreflight.Participant, len(playerIDs))
	for index, playerID := range playerIDs {
		participants[index] = tournamentpreflight.Participant{
			ParticipantID: rosterWorkflowID(20 + index), PlayerID: playerID, Seed: index + 1,
			Attendance: domain.AttendanceStateCheckedIn, ReservedTournamentID: tournamentID,
		}
	}
	input := tournamentpreflight.ReportInput{
		RosterRevision: 5, PairingRevision: 1,
		Structural: tournamentpreflight.StructuralInput{
			TournamentID: tournamentID, Preset: domain.TournamentPresetV1,
			ExpectedRosterSize: len(participants), Participants: participants,
		},
		TaskHealth: tournamentpreflight.TaskHealthInput{
			NormalPool: domain.TaskPoolRevision{Kind: domain.AssignmentTaskKindNormal},
			GoldenPool: domain.TaskPoolRevision{Kind: domain.AssignmentTaskKindGolden},
		},
		Runtime: tournamentpreflight.RuntimeInput{
			TournamentID: tournamentID, Preset: domain.TournamentPresetV1,
			RosterSize: len(participants), ContentRevision: 1,
			Clock: tournamentpreflight.ClockHealth{ObservedAt: evaluatedAt, ReferenceAt: evaluatedAt, MaxSkew: time.Second},
		},
	}
	report, err := tournamentpreflight.NewReportRevision(reportID, evaluatedAt, input)
	require.NoError(t, err)
	require.NoError(t, report.Validate())
	require.False(t, report.Passed())

	authority := RosterAuthority{
		Roster:               RosterView{ID: rosterID, TournamentID: tournamentID, Revision: 5},
		TournamentPreset:     domain.TournamentPresetV1,
		PlannedRosterSize:    len(participants),
		ContentRevision:      1,
		TournamentState:      domain.TournamentStateRegistration,
		TournamentRevision:   2,
		ProjectionRevisionID: projectionID,
		ProjectionRevision:   7,
	}
	record, err := newRosterOperationRecord(
		CommandScope{Operator: OperatorIdentity{ActorID: rosterWorkflowID(5)}, TournamentID: tournamentID, CommandID: reportID},
		RosterOperationPreflight, authority, 5, [32]byte{1},
		rosterOperationEvidence{checkedInPlayerIDs: playerIDs}, report, evaluatedAt,
	)
	require.NoError(t, err)
	command := LockRosterCommand{
		CommandScope: CommandScope{
			Operator: OperatorIdentity{ActorID: rosterWorkflowID(5)}, TournamentID: tournamentID,
			CommandID: rosterWorkflowID(6),
		},
		ExpectedProjectionRevision: 7, PreflightRevisionID: reportID, CheckedInPlayerIDs: playerIDs,
	}
	require.False(t, preflightAuthorizesLock(&record, command, authority))
}

func TestNewRosterOperationRecordRetainsLockEvidence(t *testing.T) {
	t.Parallel()

	tournamentID := rosterWorkflowID(70)
	preflightID := rosterWorkflowID(71)
	checkedIn := []uuid.UUID{
		rosterWorkflowID(72), rosterWorkflowID(73), rosterWorkflowID(74), rosterWorkflowID(75),
	}
	executedAt := time.Date(2026, time.January, 2, 4, 5, 6, 0, time.UTC)
	authority := RosterAuthority{
		Roster:          RosterView{ID: rosterWorkflowID(76), TournamentID: tournamentID, Revision: 5},
		TournamentState: domain.TournamentStateRegistration, TournamentRevision: 2,
		ProjectionRevisionID: rosterWorkflowID(77), ProjectionRevision: 8,
	}
	record, err := newRosterOperationRecord(
		CommandScope{
			Operator:     OperatorIdentity{ActorID: rosterWorkflowID(78)},
			TournamentID: tournamentID, CommandID: rosterWorkflowID(79),
		},
		RosterOperationLock, authority, authority.Roster.Revision+1, [32]byte{1},
		rosterOperationEvidence{preflightRevisionID: preflightID, checkedInPlayerIDs: checkedIn},
		map[string]any{"revision": authority.Roster.Revision + 1}, executedAt,
	)

	require.NoError(t, err)
	require.Equal(t, preflightID, record.PreflightRevisionID)
	require.Equal(t, checkedIn, record.CheckedInPlayerIDs)
	require.True(t, validRosterOperationRecord(record))
}

func validRosterWorkflowReplaceCommand() ReplaceRosterCommand {
	return NewReplaceRosterCommand(
		OperatorIdentity{ActorID: rosterWorkflowID(30)}, rosterWorkflowID(31), rosterWorkflowID(32), 7,
		[]RosterParticipantInput{
			{PlayerID: rosterWorkflowID(33), Seed: 1, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: rosterWorkflowID(34), Seed: 2, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: rosterWorkflowID(35), Seed: 3, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: rosterWorkflowID(36), Seed: 4, Attendance: domain.AttendanceStateCheckedIn},
		},
	)
}

func rosterWorkflowID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("f1000000-0000-0000-0000-%012d", value))
}
