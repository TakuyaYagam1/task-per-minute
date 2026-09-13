package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestPreflightAuthorizesLockRejectsHistoricalV1Report(t *testing.T) {
	t.Parallel()

	authority := validationRosterAuthorityFixture()
	report := validPreflightReportForLock(t, tournamentpreflight.ReportAlgorithmV1, rosterWorkflowID(80), authority.Roster.TournamentID)
	record, command := preflightLockEvidence(t, authority, report)

	require.NoError(t, report.Validate())
	require.True(t, report.Passed())
	require.False(t, preflightAuthorizesLock(&record, command, authority))
}

func TestPreflightAuthorizesLockAcceptsV2Report(t *testing.T) {
	t.Parallel()

	authority := validationRosterAuthorityFixture()
	report := validPreflightReportForLock(t, tournamentpreflight.ReportAlgorithmV2, rosterWorkflowID(81), authority.Roster.TournamentID)
	record, command := preflightLockEvidence(t, authority, report)

	require.NoError(t, report.Validate())
	require.True(t, report.Passed())
	require.True(t, preflightAuthorizesLock(&record, command, authority))
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

func validPreflightReportForLock(
	t *testing.T,
	algorithmVersion string,
	reportID uuid.UUID,
	tournamentID uuid.UUID,
) tournamentpreflight.ReportRevision {
	t.Helper()
	checks := []tournamentpreflight.Check{
		{Code: tournamentpreflight.CodeRosterComplete, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeAttendance, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeParticipantExclusive, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodePreset, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeCategories, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodePairings, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeByes, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeOverrides, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeTaskPoolsValid, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeTaskInventory, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeTaskMissing, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeTaskDisabled, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeTaskUnhealthy, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeTaskMutable, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeTaskExposed, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeTaskWrongPool, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeConfiguration, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeStorage, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeSubmission, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeTaskDelivery, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeRealtime, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeCapacity, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeClock, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeDependencies, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
		{Code: tournamentpreflight.CodeRuntimeSchedule, Passed: true, Explanation: "ok", Evidence: []string{"ok"}},
	}
	report := tournamentpreflight.ReportRevision{
		ID: reportID, TournamentID: tournamentID, AlgorithmVersion: algorithmVersion,
		EvaluatedAt:      time.Date(2026, time.January, 2, 5, 6, 7, 0, time.UTC),
		NormalizedInputs: []string{"input:stable"},
		Revisions:        []tournamentpreflight.SourceRevision{{Source: "roster", Value: "1"}},
		Checks:           checks,
	}
	report.ProofHash = preflightReportProofHash(report)
	if err := report.Validate(); err != nil {
		t.Fatalf("valid %s report: %v", algorithmVersion, err)
	}
	return report
}

func preflightLockEvidence(
	t *testing.T,
	authority RosterAuthority,
	report tournamentpreflight.ReportRevision,
) (RosterOperationRecord, LockRosterCommand) {
	t.Helper()
	checkedIn := []uuid.UUID{
		rosterWorkflowID(85), rosterWorkflowID(86), rosterWorkflowID(87), rosterWorkflowID(88),
	}
	record, err := newRosterOperationRecord(
		CommandScope{
			Operator:     OperatorIdentity{ActorID: rosterWorkflowID(82)},
			TournamentID: authority.Roster.TournamentID, CommandID: report.ID,
		},
		RosterOperationPreflight, authority, authority.Roster.Revision, [32]byte{1},
		rosterOperationEvidence{checkedInPlayerIDs: checkedIn}, report, report.EvaluatedAt,
	)
	if err != nil {
		t.Fatalf("newRosterOperationRecord() error = %v", err)
	}
	command := LockRosterCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: rosterWorkflowID(83)},
			TournamentID: authority.Roster.TournamentID, CommandID: rosterWorkflowID(84),
		},
		ExpectedProjectionRevision: authority.ProjectionRevision,
		PreflightRevisionID:        report.ID,
		CheckedInPlayerIDs:         checkedIn,
	}
	return record, command
}

func preflightReportProofHash(report tournamentpreflight.ReportRevision) string {
	type proofCheck struct {
		Code        string   `json:"code"`
		Passed      bool     `json:"passed"`
		Explanation string   `json:"explanation"`
		Evidence    []string `json:"evidence"`
	}
	type proofDocument struct {
		AlgorithmVersion string                               `json:"algorithm_version"`
		TournamentID     string                               `json:"tournament_id"`
		NormalizedInputs []string                             `json:"normalized_inputs"`
		Revisions        []tournamentpreflight.SourceRevision `json:"revisions"`
		Checks           []proofCheck                         `json:"checks"`
	}
	checks := make([]proofCheck, len(report.Checks))
	for index, check := range report.Checks {
		checks[index] = proofCheck{
			Code: string(check.Code), Passed: check.Passed, Explanation: check.Explanation,
			Evidence: append([]string(nil), check.Evidence...),
		}
	}
	document := proofDocument{
		AlgorithmVersion: report.AlgorithmVersion,
		TournamentID:     report.TournamentID.String(),
		NormalizedInputs: append([]string(nil), report.NormalizedInputs...),
		Revisions:        append([]tournamentpreflight.SourceRevision(nil), report.Revisions...),
		Checks:           checks,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validationRosterAuthorityFixture() RosterAuthority {
	tournamentID := rosterWorkflowID(1)
	return RosterAuthority{
		Roster:           RosterView{ID: rosterWorkflowID(2), TournamentID: tournamentID, Revision: 4},
		TournamentPreset: domain.TournamentPresetV1, TournamentState: domain.TournamentStateRegistration,
		TournamentRevision: 3, ProjectionRevisionID: rosterWorkflowID(3), ProjectionRevision: 9,
	}
}
