package application

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	snapshotusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

func TestUseCaseFailsClosedWithoutAdminDependencies(t *testing.T) {
	t.Parallel()

	application := AdminNewUseCase(AdminDependencies{})
	commands := validAdminCommands()
	tests := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "list tournaments", run: func(ctx context.Context) error {
			_, err := application.ListTournaments(ctx, inbound.TournamentListCommand{
				Operator: inbound.OperatorIdentity{ActorID: commands.rosterQuery.Operator.ActorID},
			})
			return err
		}},
		{name: "create tournament", run: func(ctx context.Context) error {
			_, err := application.CreateTournament(ctx, inbound.TournamentCreateCommand{
				Operator:       inbound.OperatorIdentity{ActorID: commands.rosterQuery.Operator.ActorID},
				IdempotencyKey: adminTestID(500), Preset: domain.TournamentPresetV1,
			})
			return err
		}},
		{name: "get roster", run: func(ctx context.Context) error {
			_, err := application.GetRoster(ctx, commands.rosterQuery)
			return err
		}},
		{name: "replace roster", run: func(ctx context.Context) error {
			_, err := application.ReplaceRoster(ctx, commands.replaceRoster)
			return err
		}},
		{name: "run preflight", run: func(ctx context.Context) error {
			_, err := application.RunPreflight(ctx, commands.preflight)
			return err
		}},
		{name: "lock roster", run: func(ctx context.Context) error {
			_, err := application.LockRoster(ctx, commands.lockRoster)
			return err
		}},
		{name: "unlock roster", run: func(ctx context.Context) error {
			_, err := application.UnlockRoster(ctx, commands.unlockRoster)
			return err
		}},
		{name: "configure pairings", run: func(ctx context.Context) error {
			_, err := application.ConfigurePairings(ctx, commands.pairing)
			return err
		}},
		{name: "apply tournament action", run: func(ctx context.Context) error {
			_, err := application.ApplyTournamentAction(ctx, commands.tournamentAction)
			return err
		}},
		{name: "control wave", run: func(ctx context.Context) error {
			_, err := application.ControlWave(ctx, commands.wave)
			return err
		}},
		{name: "resolve no-show", run: func(ctx context.Context) error {
			return application.ResolveNoShow(ctx, commands.noShow)
		}},
		{name: "assign reserve", run: func(ctx context.Context) error {
			return application.AssignReserve(ctx, commands.reserve)
		}},
		{name: "record forfeit", run: func(ctx context.Context) error {
			return application.RecordForfeit(ctx, commands.forfeit)
		}},
		{name: "replay game", run: func(ctx context.Context) error {
			return application.ReplayGame(ctx, commands.replay)
		}},
		{name: "correct game result", run: func(ctx context.Context) error {
			_, err := application.CorrectGameResult(ctx, commands.correction)
			return err
		}},
		{name: "list audit", run: func(ctx context.Context) error {
			_, err := application.ListAudit(ctx, commands.audit)
			return err
		}},
		{name: "export incident", run: func(ctx context.Context) error {
			_, err := application.ExportIncident(ctx, commands.incident)
			return err
		}},
		{name: "get operator snapshot", run: func(ctx context.Context) error {
			_, err := application.GetOperatorSnapshot(ctx, commands.snapshot)
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, test.run(context.Background()), domain.ErrInternal)
		})
	}
}

func TestUseCaseRejectsInvalidScopeBeforeDispatch(t *testing.T) {
	t.Parallel()

	application := AdminNewUseCase(AdminDependencies{})
	commands := validAdminCommands()
	commands.replaceRoster.CommandID = uuid.Nil
	commands.preflight.Operator.ActorID = uuid.Nil
	commands.lockRoster.CheckedInPlayerIDs[1] = commands.lockRoster.CheckedInPlayerIDs[0]
	commands.unlockRoster.Confirmed = false
	commands.pairing.Categories = append(commands.pairing.Categories, commands.pairing.Categories[0])
	commands.tournamentAction.ExpectedProjectionRevision = 0
	commands.wave.WaveID = uuid.Nil
	commands.noShow.ExpectedWindowRevisionID = uuid.Nil
	commands.reserve.ExpectedPoolRevision = 0
	commands.forfeit.EvidenceIDs = nil
	commands.replay.ExpectedClosureRevisionID = uuid.Nil
	commands.correction.Reason = "unknown"
	commands.audit.Filter.TournamentID = uuid.Nil
	commands.incident.Operator.ActorID = uuid.Nil
	commands.snapshot.TournamentID = uuid.Nil

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "replace roster", run: func() error {
			_, err := application.ReplaceRoster(context.Background(), commands.replaceRoster)
			return err
		}},
		{name: "preflight", run: func() error { _, err := application.RunPreflight(context.Background(), commands.preflight); return err }},
		{name: "lock roster", run: func() error { _, err := application.LockRoster(context.Background(), commands.lockRoster); return err }},
		{name: "unlock roster", run: func() error {
			_, err := application.UnlockRoster(context.Background(), commands.unlockRoster)
			return err
		}},
		{name: "pairing", run: func() error {
			_, err := application.ConfigurePairings(context.Background(), commands.pairing)
			return err
		}},
		{name: "tournament action", run: func() error {
			_, err := application.ApplyTournamentAction(context.Background(), commands.tournamentAction)
			return err
		}},
		{name: "wave", run: func() error { _, err := application.ControlWave(context.Background(), commands.wave); return err }},
		{name: "no-show", run: func() error { return application.ResolveNoShow(context.Background(), commands.noShow) }},
		{name: "reserve", run: func() error { return application.AssignReserve(context.Background(), commands.reserve) }},
		{name: "forfeit", run: func() error { return application.RecordForfeit(context.Background(), commands.forfeit) }},
		{name: "replay", run: func() error { return application.ReplayGame(context.Background(), commands.replay) }},
		{name: "correction", run: func() error {
			_, err := application.CorrectGameResult(context.Background(), commands.correction)
			return err
		}},
		{name: "audit", run: func() error { _, err := application.ListAudit(context.Background(), commands.audit); return err }},
		{name: "incident", run: func() error {
			_, err := application.ExportIncident(context.Background(), commands.incident)
			return err
		}},
		{name: "snapshot", run: func() error {
			_, err := application.GetOperatorSnapshot(context.Background(), commands.snapshot)
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.run()
			require.ErrorIs(t, err, domain.ErrValidation)
			require.NotErrorIs(t, err, domain.ErrInternal)
		})
	}
}

func TestSwissRoundViewValidation(t *testing.T) {
	t.Parallel()

	tournamentID := adminTestID(1)
	view := validSwissRoundViewFixture(tournamentID)
	require.True(t, executionusecase.ValidSwissRoundView(view, tournamentID, 1))

	tests := []struct {
		name   string
		mutate func(*executionusecase.SwissRoundView)
	}{
		{name: "invalid evidence digest", mutate: func(value *executionusecase.SwissRoundView) { value.PairingEvidence.ReplayDigest = "not-a-digest" }},
		{name: "foreign pairing participant", mutate: func(value *executionusecase.SwissRoundView) { value.Pairings[0].FirstParticipantID = adminTestID(900) }},
		{name: "incomplete participant coverage", mutate: func(value *executionusecase.SwissRoundView) { value.Pairings = value.Pairings[:1] }},
		{name: "duplicate standing position", mutate: func(value *executionusecase.SwissRoundView) {
			value.Standings[1].Position = value.Standings[0].Position
		}},
		{name: "lossy bye", mutate: func(value *executionusecase.SwissRoundView) {
			value.RosterParticipantIDs = append(value.RosterParticipantIDs, adminTestID(50))
			value.Standings = append(value.Standings, pairingusecase.SwissStandingView{
				ParticipantID: adminTestID(50), Position: 5, PointsLabel: "provisional",
				BuchholzStatus: "provisional", StableSeed: 5,
			})
			value.Bye = &executionusecase.SwissByeView{
				ID: adminTestID(51), RoundID: value.ID, ParticipantID: adminTestID(50),
				PointsAwarded: 2, RevisionID: adminTestID(52), EvidenceID: adminTestID(53),
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneSwissRoundView(view)
			test.mutate(&candidate)
			require.False(t, executionusecase.ValidSwissRoundView(candidate, tournamentID, 1))
		})
	}
}

func TestCorrectionPatchValidation(t *testing.T) {
	t.Parallel()

	winnerID := adminTestID(1)
	submissionID := adminTestID(2)
	solvedAt := adminTestTime()
	solved := correctionusecase.CorrectionPatch{
		State: domain.GameStateCompleted, Reason: domain.GameResultReasonSolved, WinnerID: &winnerID,
		SolvedAt: &solvedAt, SubmissionID: &submissionID, EvidenceDigest: [32]byte{1},
	}
	require.True(t, correctionusecase.ValidCorrectionPatch(solved))

	forfeit := correctionusecase.CorrectionPatch{
		State: domain.GameStateCompleted, Reason: domain.GameResultReasonOperatorForfeit, WinnerID: &winnerID,
	}
	require.True(t, correctionusecase.ValidCorrectionPatch(forfeit))

	forfeit.EvidenceDigest = [32]byte{1}
	require.False(t, correctionusecase.ValidCorrectionPatch(forfeit), "non-solve metadata must remain empty")
	solved.WinnerID = nil
	require.False(t, correctionusecase.ValidCorrectionPatch(solved), "completed result requires a winner")
}

func TestNormalizeAdminErrorRequiresCompleteRevisionEvidence(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, normalizeAdminError(domain.ErrConflict), domain.ErrInternal)
	require.ErrorIs(t, normalizeAdminError(&operationusecase.RevisionConflictError{
		ExpectedRevision: 1,
		CurrentRevision:  2,
		CurrentState:     domain.TournamentState("unknown"),
	}), domain.ErrInternal)

	conflict := &operationusecase.RevisionConflictError{
		ExpectedRevision: 1,
		CurrentRevision:  2,
		CurrentState:     domain.TournamentStateSwiss,
	}
	require.Same(t, conflict, normalizeAdminError(conflict))
}

func TestNormalizeAdminErrorReportsAssignmentCapacity(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("draft branch: %w: %w", assignmentusecase.ErrInvalidExactNormalAssignment, domain.ErrConflict)
	require.ErrorIs(t, normalizeAdminError(err), domain.ErrInvalidContentConfiguration)
}

type adminCommands struct {
	rosterQuery      rosterusecase.RosterQuery
	replaceRoster    rosterusecase.ReplaceRosterCommand
	preflight        rosterusecase.PreflightCommand
	lockRoster       rosterusecase.LockRosterCommand
	unlockRoster     rosterusecase.UnlockRosterCommand
	pairing          pairingusecase.PairingCommand
	tournamentAction lifecycleusecase.TournamentActionCommand
	wave             executionusecase.WaveCommand
	noShow           resultusecase.NoShowCommand
	reserve          replayusecase.ReserveCommand
	forfeit          resultusecase.ForfeitCommand
	replay           replayusecase.ReplayCommand
	correction       correctionusecase.CorrectionCommand
	audit            incidentusecase.AuditQuery
	incident         incidentusecase.IncidentQuery
	snapshot         snapshotusecase.SnapshotQuery
}

func validAdminCommands() adminCommands {
	operator := operationusecase.OperatorIdentity{ActorID: adminTestID(1)}
	tournamentID := adminTestID(2)
	scope := func(id int) operationusecase.CommandScope {
		return operationusecase.CommandScope{Operator: operator, TournamentID: tournamentID, CommandID: adminTestID(id)}
	}
	players := []uuid.UUID{adminTestID(20), adminTestID(21), adminTestID(22), adminTestID(23)}
	participants := make([]rosterusecase.RosterParticipantInput, len(players))
	for index, playerID := range players {
		participants[index] = rosterusecase.RosterParticipantInput{
			PlayerID: playerID, Seed: index + 1, Attendance: domain.AttendanceStateCheckedIn,
		}
	}
	winnerID := adminTestID(70)
	submissionID := adminTestID(71)
	solvedAt := adminTestTime()
	return adminCommands{
		rosterQuery: rosterusecase.RosterQuery{Operator: operator, TournamentID: tournamentID},
		replaceRoster: rosterusecase.NewReplaceRosterCommand(
			operator, tournamentID, adminTestID(3), 1, participants,
		),
		preflight: rosterusecase.PreflightCommand{CommandScope: scope(4), ExpectedProjectionRevision: 1},
		lockRoster: rosterusecase.LockRosterCommand{
			CommandScope: scope(5), ExpectedProjectionRevision: 1,
			PreflightRevisionID: adminTestID(30), CheckedInPlayerIDs: append([]uuid.UUID(nil), players...),
		},
		unlockRoster: rosterusecase.UnlockRosterCommand{
			CommandScope: scope(6), ExpectedProjectionRevision: 1, Confirmed: true, Reason: "operator correction",
		},
		pairing: pairingusecase.PairingCommand{
			CommandScope: scope(7), ExpectedProjectionRevision: 1, RoundNumber: 1,
			PairingMode: pairingusecase.PairingModeAutomatic, CategoryMode: domain.CategoryModeRandom,
			Categories: []domain.Category{domain.CategoryWeb},
		},
		tournamentAction: lifecycleusecase.TournamentActionCommand{
			CommandScope: scope(8), ExpectedProjectionRevision: 1,
			Action: lifecycleusecase.TournamentActionOpenRegistration, Confirmed: true,
		},
		wave: executionusecase.WaveCommand{
			CommandScope: scope(9), WaveID: adminTestID(31), ExpectedProjectionRevision: 1,
			Action: executionusecase.WaveActionOpenReadyWindow, Confirmed: true,
		},
		noShow: resultusecase.NoShowCommand{
			CommandScope: scope(10), WaveID: adminTestID(31), WindowID: adminTestID(32),
			SeriesID: adminTestID(33), Confirmed: true, Reason: "ready window elapsed",
			ExpectedAuthorityRevision: 1, ExpectedWaveRevisionID: adminTestID(34),
			ExpectedWindowRevisionID: adminTestID(35), ExpectedSeriesState: domain.SeriesStateReady,
			GameResultRevisionIDs: []uuid.UUID{adminTestID(36)}, ScoreRevisionID: adminTestID(37),
			SeriesResultRevisionID: adminTestID(38),
		},
		reserve: replayusecase.ReserveCommand{
			CommandScope: scope(11), OldWaveID: adminTestID(40), SeriesID: adminTestID(41),
			SlotID: adminTestID(42), AssignmentID: adminTestID(43), AssignmentAttemptID: adminTestID(44),
			Confirmed: true, Reason: "approved reserve", ExpectedAuthorityRevision: 1,
			ExpectedExhaustionCommandID: adminTestID(45), ExpectedAssignmentRevision: 1,
			ExpectedPoolRevisionID: adminTestID(46), ExpectedPoolRevision: 1,
			ExpectedHistoryRevisionID: adminTestID(47), ExpectedHistoryRevision: 1,
			ExpectedArtifactRevisionID: adminTestID(48), ExpectedArtifactRevision: 1,
			ExpectedReservationRevisionID: adminTestID(49), ExpectedReservationRevision: 1,
			ExpectedCategoryRevisionID: adminTestID(50), ExpectedCategoryRevision: 1,
			ProposedTaskID: adminTestID(51), ProposedVersion: 1, ProposedSnapshotID: adminTestID(52),
			ExpectedSnapshotID: adminTestID(53), EvidenceID: adminTestID(54),
		},
		forfeit: resultusecase.ForfeitCommand{
			CommandScope: scope(12), SeriesID: adminTestID(60), ForfeitingParticipantID: adminTestID(61),
			Confirmed: true, Reason: "confirmed rule violation", ExpectedAuthorityRevision: 1,
			Basis: "rule_violation", RuleID: "rules.forfeit.1", EvidenceIDs: []uuid.UUID{adminTestID(62)},
			ScoreRevisionID: adminTestID(63), SeriesResultRevisionID: adminTestID(64),
			AuditEventID: adminTestID(65), OutboxEventID: adminTestID(66), ProjectionRevisionID: adminTestID(67),
		},
		replay: replayusecase.ReplayCommand{
			CommandScope: scope(13), OldWaveID: adminTestID(80), SeriesID: adminTestID(81),
			SlotID: adminTestID(82), AssignmentID: adminTestID(83), FailedGameID: adminTestID(84),
			Confirmed: true, Reason: "replace failed game", ExpectedAuthorityRevision: 1,
			ExpectedClosureRevisionID: adminTestID(85), AssignmentAttemptID: adminTestID(86),
			ReplacementGameID: adminTestID(87), ReplacementWaveID: adminTestID(88),
			ReplacementWaveRevisionID: adminTestID(89), ReadyWindowID: adminTestID(90),
			ReadyWindowRevisionID: adminTestID(91),
		},
		correction: correctionusecase.CorrectionCommand{
			CommandScope: scope(14), SeriesID: adminTestID(100), GameID: adminTestID(101), SourceResultRevision: adminTestID(102),
			ExpectedProjectionRevision: 1, Confirmed: true, Reason: "operator_ruling",
			Explanation: "verified result correction", Fields: []string{"winner"},
			Patch: correctionusecase.CorrectionPatch{
				State: domain.GameStateCompleted, Reason: domain.GameResultReasonSolved, WinnerID: &winnerID,
				SolvedAt: &solvedAt, SubmissionID: &submissionID, EvidenceDigest: [32]byte{1},
			},
		},
		audit: incidentusecase.AuditQuery{
			Operator: operator, Filter: audit.AuditFilter{TournamentID: tournamentID},
		},
		incident: incidentusecase.IncidentQuery{Operator: operator, TournamentID: tournamentID},
		snapshot: snapshotusecase.SnapshotQuery{Operator: operator, TournamentID: tournamentID},
	}
}

func validSwissRoundViewFixture(tournamentID uuid.UUID) executionusecase.SwissRoundView {
	roundID := adminTestID(200)
	participants := []uuid.UUID{adminTestID(201), adminTestID(202), adminTestID(203), adminTestID(204)}
	decidedAt := adminTestTime()
	evidenceID := adminTestID(205)
	inputs := []string{
		"participant:" + participants[0].String(),
		"participant:" + participants[1].String(),
		"participant:" + participants[2].String(),
		"participant:" + participants[3].String(),
	}
	return executionusecase.SwissRoundView{
		ID: roundID, TournamentID: tournamentID, RoundNumber: 1, Revision: 1,
		RosterParticipantIDs: participants,
		PairingEvidence: &executionusecase.SwissPairingEvidenceView{
			ID: evidenceID, Purpose: string(domain.DecisionPurposePairing),
			AlgorithmVersion: domain.DecisionAlgorithmV1, NormalizedInputs: inputs,
			Result:       []string{inputs[2], inputs[0], inputs[3], inputs[1]},
			ReplayDigest: fmt.Sprintf("%064x", 1), OwnerID: roundID, DecidedAt: decidedAt,
		},
		Pairings: []executionusecase.SwissPairingView{
			{ID: adminTestID(210), RoundID: roundID, FirstParticipantID: participants[0], SecondParticipantID: participants[1], EvidenceID: evidenceID},
			{ID: adminTestID(211), RoundID: roundID, FirstParticipantID: participants[2], SecondParticipantID: participants[3], EvidenceID: evidenceID},
		},
		Standings: []pairingusecase.SwissStandingView{
			{ParticipantID: participants[0], Position: 1, PointsLabel: "provisional", BuchholzStatus: "provisional", StableSeed: 1},
			{ParticipantID: participants[1], Position: 2, PointsLabel: "provisional", BuchholzStatus: "provisional", StableSeed: 2},
			{ParticipantID: participants[2], Position: 3, PointsLabel: "provisional", BuchholzStatus: "provisional", StableSeed: 3},
			{ParticipantID: participants[3], Position: 4, PointsLabel: "provisional", BuchholzStatus: "provisional", StableSeed: 4},
		},
		CreatedAt: decidedAt, UpdatedAt: decidedAt,
	}
}

func cloneSwissRoundView(view executionusecase.SwissRoundView) executionusecase.SwissRoundView {
	clone := view
	clone.RosterParticipantIDs = append([]uuid.UUID(nil), view.RosterParticipantIDs...)
	clone.Pairings = append([]executionusecase.SwissPairingView(nil), view.Pairings...)
	clone.Standings = append([]pairingusecase.SwissStandingView(nil), view.Standings...)
	if view.PairingEvidence != nil {
		evidence := *view.PairingEvidence
		evidence.NormalizedInputs = append([]string(nil), view.PairingEvidence.NormalizedInputs...)
		evidence.Result = append([]string(nil), view.PairingEvidence.Result...)
		clone.PairingEvidence = &evidence
	}
	if view.Bye != nil {
		bye := *view.Bye
		clone.Bye = &bye
	}
	return clone
}

func adminTestID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", value))
}

func adminTestTime() time.Time {
	return time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
}
