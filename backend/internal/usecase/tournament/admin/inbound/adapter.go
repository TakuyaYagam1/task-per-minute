package inbound

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/google/uuid"
)

// NewInboundAdapter exposes the admin workflow through its consumer-owned
// inbound contract. Workflow records and leaf-usecase values remain private.
func NewInboundAdapter(next Service) inbound.TournamentAdminUseCase {
	return &inboundAdapter{next: next}
}

type inboundAdapter struct{ next Service }

var _ inbound.TournamentAdminUseCase = (*inboundAdapter)(nil)

func (a *inboundAdapter) GetRoster(ctx context.Context, query inbound.AdminRosterQuery) (inbound.AdminRosterView, error) {
	view, err := a.next.GetRoster(ctx, RosterQuery{Operator: operatorIdentity(query.Operator), TournamentID: query.TournamentID})
	return rosterView(view), adminInboundError(err)
}

func (a *inboundAdapter) ReplaceRoster(ctx context.Context, command inbound.AdminReplaceRosterCommand) (inbound.AdminRosterView, error) {
	participants := make([]RosterParticipantInput, len(command.Participants))
	for i, item := range command.Participants {
		participants[i] = RosterParticipantInput{PlayerID: item.PlayerID, Seed: item.Seed, Attendance: item.Attendance}
	}
	view, err := a.next.ReplaceRoster(ctx, ReplaceRosterCommand{CommandScope: commandScope(command.AdminCommandScope), ExpectedProjectionRevision: command.ExpectedProjectionRevision, Participants: participants})
	return rosterView(view), adminInboundError(err)
}

func (a *inboundAdapter) RunPreflight(ctx context.Context, command inbound.AdminPreflightCommand) (inbound.AdminPreflightReport, error) {
	report, err := a.next.RunPreflight(ctx, PreflightCommand{CommandScope: commandScope(command.AdminCommandScope), ExpectedProjectionRevision: command.ExpectedProjectionRevision})
	return mapPreflightReport(report), adminInboundError(err)
}

func (a *inboundAdapter) LockRoster(ctx context.Context, command inbound.AdminLockRosterCommand) (inbound.AdminRosterView, error) {
	view, err := a.next.LockRoster(ctx, LockRosterCommand{CommandScope: commandScope(command.AdminCommandScope), ExpectedProjectionRevision: command.ExpectedProjectionRevision, PreflightRevisionID: command.PreflightRevisionID, CheckedInPlayerIDs: append([]uuid.UUID(nil), command.CheckedInPlayerIDs...)})
	return rosterView(view), adminInboundError(err)
}

func (a *inboundAdapter) UnlockRoster(ctx context.Context, command inbound.AdminUnlockRosterCommand) (inbound.AdminRosterView, error) {
	view, err := a.next.UnlockRoster(ctx, UnlockRosterCommand{CommandScope: commandScope(command.AdminCommandScope), ExpectedProjectionRevision: command.ExpectedProjectionRevision, Confirmed: command.Confirmed, Reason: command.Reason})
	return rosterView(view), adminInboundError(err)
}

func (a *inboundAdapter) ConfigurePairings(ctx context.Context, command inbound.AdminPairingCommand) (inbound.AdminSwissRoundView, error) {
	pairs := make([]ParticipantPair, len(command.ManualPairings))
	for i, pair := range command.ManualPairings {
		pairs[i] = ParticipantPair{FirstParticipantID: pair.FirstParticipantID, SecondParticipantID: pair.SecondParticipantID}
	}
	view, err := a.next.ConfigurePairings(ctx, PairingCommand{CommandScope: commandScope(command.AdminCommandScope), ExpectedProjectionRevision: command.ExpectedProjectionRevision, RoundNumber: command.RoundNumber, PairingMode: PairingMode(command.PairingMode), CategoryMode: command.CategoryMode, Categories: append([]domain.Category(nil), command.Categories...), ManualPairings: pairs, ManualPairingsProvided: command.ManualPairingsProvided, ManualByeParticipantID: cloneUUID(command.ManualByeParticipantID)})
	return swissRoundView(view), adminInboundError(err)
}

func (a *inboundAdapter) ApplyTournamentAction(ctx context.Context, command inbound.AdminTournamentActionCommand) (inbound.TournamentView, error) {
	view, err := a.next.ApplyTournamentAction(ctx, TournamentActionCommand{CommandScope: commandScope(command.AdminCommandScope), ExpectedProjectionRevision: command.ExpectedProjectionRevision, Action: TournamentAction(command.Action), Confirmed: command.Confirmed, Reason: command.Reason})
	return view, adminInboundError(err)
}

func (a *inboundAdapter) ControlWave(ctx context.Context, command inbound.AdminWaveCommand) (inbound.AdminWaveView, error) {
	view, err := a.next.ControlWave(ctx, WaveCommand{CommandScope: commandScope(command.AdminCommandScope), WaveID: command.WaveID, ExpectedProjectionRevision: command.ExpectedProjectionRevision, Action: WaveAction(command.Action), Confirmed: command.Confirmed, Reason: command.Reason})
	return waveView(view), adminInboundError(err)
}

func (a *inboundAdapter) ResolveNoShow(ctx context.Context, command inbound.AdminNoShowCommand) error {
	return adminInboundError(a.next.ResolveNoShow(ctx, NoShowCommand{CommandScope: commandScope(command.AdminCommandScope), WaveID: command.WaveID, WindowID: command.WindowID, SeriesID: command.SeriesID, Confirmed: command.Confirmed, Reason: command.Reason, ExpectedAuthorityRevision: command.ExpectedAuthorityRevision, ExpectedWaveRevisionID: command.ExpectedWaveRevisionID, ExpectedWindowRevisionID: command.ExpectedWindowRevisionID, ExpectedSeriesState: command.ExpectedSeriesState, GameResultRevisionIDs: append([]uuid.UUID(nil), command.GameResultRevisionIDs...), ScoreRevisionID: command.ScoreRevisionID, SeriesResultRevisionID: command.SeriesResultRevisionID}))
}

func (a *inboundAdapter) AssignReserve(ctx context.Context, c inbound.AdminReserveCommand) error {
	return adminInboundError(a.next.AssignReserve(ctx, ReserveCommand{CommandScope: commandScope(c.AdminCommandScope), OldWaveID: c.OldWaveID, SeriesID: c.SeriesID, SlotID: c.SlotID, AssignmentID: c.AssignmentID, AssignmentAttemptID: c.AssignmentAttemptID, Confirmed: c.Confirmed, Reason: c.Reason, ExpectedAuthorityRevision: c.ExpectedAuthorityRevision, ExpectedExhaustionCommandID: c.ExpectedExhaustionCommandID, ExpectedAssignmentRevision: c.ExpectedAssignmentRevision, ProposedTaskID: c.ProposedTaskID, ProposedVersion: c.ProposedVersion, ProposedSnapshotID: c.ProposedSnapshotID, ExpectedSnapshotID: c.ExpectedSnapshotID, EvidenceID: c.EvidenceID, ExpectedPoolRevisionID: c.ExpectedPoolRevisionID, ExpectedPoolRevision: c.ExpectedPoolRevision, ExpectedHistoryRevisionID: c.ExpectedHistoryRevisionID, ExpectedHistoryRevision: c.ExpectedHistoryRevision, ExpectedArtifactRevisionID: c.ExpectedArtifactRevisionID, ExpectedArtifactRevision: c.ExpectedArtifactRevision, ExpectedReservationRevisionID: c.ExpectedReservationRevisionID, ExpectedReservationRevision: c.ExpectedReservationRevision, ExpectedCategoryRevisionID: c.ExpectedCategoryRevisionID, ExpectedCategoryRevision: c.ExpectedCategoryRevision}))
}

func (a *inboundAdapter) RecordForfeit(ctx context.Context, c inbound.AdminForfeitCommand) error {
	var game *GameExpectation
	if c.ExpectedGame != nil {
		game = &GameExpectation{SlotID: c.ExpectedGame.SlotID, GameID: c.ExpectedGame.GameID, AttemptNo: c.ExpectedGame.AttemptNo, State: c.ExpectedGame.State}
	}
	return a.next.RecordForfeit(ctx, ForfeitCommand{CommandScope: commandScope(c.AdminCommandScope), SeriesID: c.SeriesID, ForfeitingParticipantID: c.ForfeitingParticipantID, Confirmed: c.Confirmed, Reason: c.Reason, ExpectedAuthorityRevision: c.ExpectedAuthorityRevision, ExpectedGame: game, Basis: c.Basis, RuleID: c.RuleID, EvidenceIDs: append([]uuid.UUID(nil), c.EvidenceIDs...), GameResultRevisionID: cloneUUID(c.GameResultRevisionID), ScoreRevisionID: c.ScoreRevisionID, SeriesResultRevisionID: c.SeriesResultRevisionID, AuditEventID: c.AuditEventID, OutboxEventID: c.OutboxEventID, ProjectionRevisionID: c.ProjectionRevisionID})
}

func (a *inboundAdapter) ReplayGame(ctx context.Context, c inbound.AdminReplayCommand) error {
	return a.next.ReplayGame(ctx, ReplayCommand{CommandScope: commandScope(c.AdminCommandScope), OldWaveID: c.OldWaveID, SeriesID: c.SeriesID, SlotID: c.SlotID, AssignmentID: c.AssignmentID, FailedGameID: c.FailedGameID, Confirmed: c.Confirmed, Reason: c.Reason, ExpectedAuthorityRevision: c.ExpectedAuthorityRevision, ExpectedClosureRevisionID: c.ExpectedClosureRevisionID, AssignmentAttemptID: c.AssignmentAttemptID, ReplacementGameID: c.ReplacementGameID, ReplacementWaveID: c.ReplacementWaveID, ReplacementWaveRevisionID: c.ReplacementWaveRevisionID, ReadyWindowID: c.ReadyWindowID, ReadyWindowRevisionID: c.ReadyWindowRevisionID})
}

func (a *inboundAdapter) CorrectGameResult(ctx context.Context, c inbound.AdminCorrectionCommand) (inbound.AdminCorrectionEvidence, error) {
	command := correctionCommand(c)
	evidence, err := a.next.CorrectGameResult(ctx, command)
	return inboundCorrectionEvidence(evidence), adminInboundError(err)
}

func (a *inboundAdapter) ListAudit(ctx context.Context, query inbound.AdminAuditQuery) (inbound.AdminAuditPage, error) {
	page, err := a.next.ListAudit(ctx, AuditQuery{Operator: operatorIdentity(query.Operator), Filter: auditFilter(query.Filter)})
	return mapAuditPage(page), err
}

func (a *inboundAdapter) ExportIncident(ctx context.Context, query inbound.AdminIncidentQuery) (inbound.AdminIncidentBundle, error) {
	bundle, err := a.next.ExportIncident(ctx, IncidentQuery{Operator: operatorIdentity(query.Operator), TournamentID: query.TournamentID})
	return mapIncidentBundle(bundle), err
}

func (a *inboundAdapter) GetOperatorSnapshot(ctx context.Context, query inbound.AdminSnapshotQuery) (inbound.AdminOperatorSnapshotView, error) {
	var cursor *OperatorCursor
	if query.Cursor != nil {
		cursor = &OperatorCursor{ProjectionRevision: query.Cursor.ProjectionRevision, AuthorityRevision: query.Cursor.AuthorityRevision, AuditSequence: query.Cursor.AuditSequence}
	}
	view, err := a.next.GetOperatorSnapshot(ctx, SnapshotQuery{Operator: operatorIdentity(query.Operator), TournamentID: query.TournamentID, Cursor: cursor})
	return operatorSnapshotView(view), adminInboundError(err)
}
