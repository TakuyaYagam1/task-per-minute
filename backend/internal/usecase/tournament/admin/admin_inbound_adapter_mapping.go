package admin

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func adminInboundError(err error) error {
	var conflict *RevisionConflictError
	if errors.As(err, &conflict) {
		return &inbound.AdminRevisionConflictError{ExpectedRevision: conflict.ExpectedRevision, CurrentRevision: conflict.CurrentRevision, CurrentState: conflict.CurrentState}
	}
	return err
}

func operatorIdentity(value inbound.AdminOperatorIdentity) OperatorIdentity {
	return OperatorIdentity{ActorID: value.ActorID}
}

func commandScope(value inbound.AdminCommandScope) CommandScope {
	return CommandScope{Operator: operatorIdentity(value.Operator), TournamentID: value.TournamentID, CommandID: value.CommandID}
}

func rosterView(value RosterView) inbound.AdminRosterView {
	mapped := inbound.AdminRosterView{ID: value.ID, TournamentID: value.TournamentID, Revision: value.Revision, Locked: value.Locked, ExecutionStarted: value.ExecutionStarted, LockedAt: cloneTime(value.LockedAt), ExecutionStartedAt: cloneTime(value.ExecutionStartedAt), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
	if value.Participants != nil {
		mapped.Participants = make([]inbound.AdminRosterParticipantView, len(value.Participants))
	}
	for i, participant := range value.Participants {
		mapped.Participants[i] = inbound.AdminRosterParticipantView{ID: participant.ID, RosterID: participant.RosterID, TournamentID: participant.TournamentID, PlayerID: participant.PlayerID, Seed: participant.Seed, Attendance: participant.Attendance, CreatedAt: participant.CreatedAt, UpdatedAt: participant.UpdatedAt}
	}
	return mapped
}

func swissRoundView(value SwissRoundView) inbound.AdminSwissRoundView {
	mapped := inbound.AdminSwissRoundView{ID: value.ID, TournamentID: value.TournamentID, RoundNumber: value.RoundNumber, Revision: value.Revision, RosterParticipantIDs: append([]uuid.UUID(nil), value.RosterParticipantIDs...), Locked: value.Locked, LockedAt: cloneTime(value.LockedAt), StartedAt: cloneTime(value.StartedAt), CompletedAt: cloneTime(value.CompletedAt), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
	if value.PairingEvidence != nil {
		mapped.PairingEvidence = &inbound.AdminSwissPairingEvidenceView{ID: value.PairingEvidence.ID, Purpose: value.PairingEvidence.Purpose, AlgorithmVersion: value.PairingEvidence.AlgorithmVersion, NormalizedInputs: append([]string(nil), value.PairingEvidence.NormalizedInputs...), Result: append([]string(nil), value.PairingEvidence.Result...), ReplayDigest: value.PairingEvidence.ReplayDigest, OwnerID: value.PairingEvidence.OwnerID, DecidedAt: value.PairingEvidence.DecidedAt}
	}
	if value.Pairings != nil {
		mapped.Pairings = make([]inbound.AdminSwissPairingView, len(value.Pairings))
	}
	for i, pair := range value.Pairings {
		mapped.Pairings[i] = inbound.AdminSwissPairingView{ID: pair.ID, RoundID: pair.RoundID, FirstParticipantID: pair.FirstParticipantID, SecondParticipantID: pair.SecondParticipantID, EvidenceID: pair.EvidenceID, Repeated: pair.Repeated, OverrideActorID: cloneUUID(pair.OverrideActorID), OverrideReason: copyString(pair.OverrideReason)}
	}
	if value.Bye != nil {
		mapped.Bye = &inbound.AdminSwissByeView{ID: value.Bye.ID, RoundID: value.Bye.RoundID, ParticipantID: value.Bye.ParticipantID, PointsAwarded: value.Bye.PointsAwarded, RevisionID: value.Bye.RevisionID, EvidenceID: value.Bye.EvidenceID}
	}
	if value.Standings != nil {
		mapped.Standings = make([]inbound.AdminSwissStandingView, len(value.Standings))
	}
	for i, standing := range value.Standings {
		mapped.Standings[i] = inbound.AdminSwissStandingView{ParticipantID: standing.ParticipantID, Position: standing.Position, Points: standing.Points, PointsLabel: standing.PointsLabel, Buchholz: standing.Buchholz, BuchholzStatus: standing.BuchholzStatus, HeadToHeadPoints: standing.HeadToHeadPoints, HeadToHeadApplied: standing.HeadToHeadApplied, EffectiveTimeMS: standing.EffectiveTimeMS, AcceptedSolveTimeMS: copyInt64(standing.AcceptedSolveTimeMS), StableSeed: standing.StableSeed}
	}
	return mapped
}

func waveView(value WaveView) inbound.AdminWaveView {
	mapped := inbound.AdminWaveView{Wave: cloneWave(value.Wave), Revision: value.Revision, ByeParticipantID: cloneUUID(value.ByeParticipantID)}
	if value.ReadinessRevisions != nil {
		mapped.ReadinessRevisions = make(map[uuid.UUID]int64, len(value.ReadinessRevisions))
		for id, revision := range value.ReadinessRevisions {
			mapped.ReadinessRevisions[id] = revision
		}
	}
	if value.SeriesIDs != nil {
		mapped.SeriesIDs = make(map[uuid.UUID]uuid.UUID, len(value.SeriesIDs))
		for participantID, seriesID := range value.SeriesIDs {
			mapped.SeriesIDs[participantID] = seriesID
		}
	}
	return mapped
}

func correctionCommand(value inbound.AdminCorrectionCommand) CorrectionCommand {
	mapped := CorrectionCommand{CommandScope: commandScope(value.AdminCommandScope), SeriesID: value.SeriesID, GameID: value.GameID, ExpectedProjectionRevision: value.ExpectedProjectionRevision, Confirmed: value.Confirmed, Reason: value.Reason, Explanation: value.Explanation, Fields: append([]string(nil), value.Fields...), Patch: CorrectionPatch{State: value.Patch.State, Reason: value.Patch.Reason, WinnerID: cloneUUID(value.Patch.WinnerID), SolvedAt: cloneTime(value.Patch.SolvedAt), SubmissionID: cloneUUID(value.Patch.SubmissionID), EvidenceDigest: value.Patch.EvidenceDigest}}
	if value.ProjectionIntents != nil {
		mapped.ProjectionIntents = make([]CorrectionProjectionIntent, len(value.ProjectionIntents))
	}
	for i, intent := range value.ProjectionIntents {
		mapped.ProjectionIntents[i] = CorrectionProjectionIntent{ExpectedRevision: ProjectionRevisionExpectation{ID: intent.ExpectedRevision.ID, TournamentID: intent.ExpectedRevision.TournamentID, ArtifactKind: intent.ExpectedRevision.ArtifactKind, ArtifactID: intent.ExpectedRevision.ArtifactID, RevisionNo: intent.ExpectedRevision.RevisionNo, PreviousRevisionID: cloneUUID(intent.ExpectedRevision.PreviousRevisionID), PayloadDigest: intent.ExpectedRevision.PayloadDigest, CreatedAt: intent.ExpectedRevision.CreatedAt}, NextRevisionID: intent.NextRevisionID, DecisionID: intent.DecisionID, PayloadDigest: intent.PayloadDigest}
	}
	if value.UnlockIntents != nil {
		mapped.UnlockIntents = make([]CorrectionUnlockIntent, len(value.UnlockIntents))
	}
	for i, intent := range value.UnlockIntents {
		mapped.UnlockIntents[i] = CorrectionUnlockIntent{ReservationID: intent.ReservationID, TournamentID: intent.TournamentID, OwnerID: intent.OwnerID, SourceRevisionID: intent.SourceRevisionID, ExpectedRevision: intent.ExpectedRevision, ExpectedUsed: intent.ExpectedUsed, ExpectedDisclosed: intent.ExpectedDisclosed, EvidenceDigest: intent.EvidenceDigest, BindingDigest: intent.BindingDigest}
	}
	return mapped
}

func inboundCorrectionEvidence(value CorrectionEvidence) inbound.AdminCorrectionEvidence {
	mapped := inbound.AdminCorrectionEvidence{CommandID: value.CommandID, TournamentID: value.TournamentID, SeriesID: value.SeriesID, GameID: value.GameID, OperatorID: value.OperatorID, Reason: value.Reason, Fields: append([]string(nil), value.Fields...), RequestedAt: value.RequestedAt, ValidationDigest: value.ValidationDigest}
	if value.Supersessions != nil {
		mapped.Supersessions = make([]inbound.AdminProjectionSupersessionView, len(value.Supersessions))
	}
	for i, item := range value.Supersessions {
		mapped.Supersessions[i] = inbound.AdminProjectionSupersessionView{ArtifactKind: item.ArtifactKind, ArtifactID: item.ArtifactID, PreviousRevisionID: item.PreviousRevisionID, SuccessorRevisionID: item.SuccessorRevisionID, PreviousDecisionID: cloneUUID(item.PreviousDecisionID), ReplacementDecisionID: item.ReplacementDecisionID}
	}
	if value.UnlockIntents != nil {
		mapped.UnlockIntents = make([]inbound.AdminCorrectionUnlockIntent, len(value.UnlockIntents))
	}
	for i, item := range value.UnlockIntents {
		mapped.UnlockIntents[i] = inbound.AdminCorrectionUnlockIntent{ReservationID: item.ReservationID, TournamentID: item.TournamentID, OwnerID: item.OwnerID, SourceRevisionID: item.SourceRevisionID, ExpectedRevision: item.ExpectedRevision, ExpectedUsed: item.ExpectedUsed, ExpectedDisclosed: item.ExpectedDisclosed, EvidenceDigest: item.EvidenceDigest, BindingDigest: item.BindingDigest}
	}
	return mapped
}

func auditFilter(value inbound.AdminAuditFilter) audit.AuditFilter {
	var cursor *audit.Cursor
	if value.Cursor != nil {
		cursor = &audit.Cursor{OccurredAt: value.Cursor.OccurredAt, AuditEventID: value.Cursor.AuditEventID, RevisionID: value.Cursor.RevisionID, SnapshotBound: value.Cursor.SnapshotBound}
	}
	return audit.AuditFilter{TournamentID: value.TournamentID, EntityKind: audit.EntityKind(value.EntityKind), EntityID: cloneUUID(value.EntityID), EventType: value.EventType, ActorKind: value.ActorKind, ActorID: cloneUUID(value.ActorID), ResultReason: value.ResultReason, OccurredFrom: cloneTime(value.OccurredFrom), OccurredTo: cloneTime(value.OccurredTo), Cursor: cursor, PageSize: value.PageSize}
}
func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneWave(value domain.Wave) domain.Wave {
	clone := value
	clone.Members = append([]domain.WaveMember(nil), value.Members...)
	clone.StartedAt = cloneTime(value.StartedAt)
	clone.PausedAt = cloneTime(value.PausedAt)
	clone.ReadyWindow = cloneReadyWindow(value.ReadyWindow)
	return clone
}
func cloneReadyWindow(value *domain.ReadyWindow) *domain.ReadyWindow {
	if value == nil {
		return nil
	}
	clone := *value
	clone.ConsumedAt = cloneTime(value.ConsumedAt)
	return &clone
}

func operatorSnapshotView(value OperatorSnapshotView) inbound.AdminOperatorSnapshotView {
	mapped := inbound.AdminOperatorSnapshotView{Tournament: value.Tournament, Roster: rosterView(value.Roster), NextCursor: inbound.AdminOperatorCursor{ProjectionRevision: value.NextCursor.ProjectionRevision, AuthorityRevision: value.NextCursor.AuthorityRevision, AuditSequence: value.NextCursor.AuditSequence}}
	if value.Waves != nil {
		mapped.Waves = make([]inbound.AdminWaveView, len(value.Waves))
		for i, wave := range value.Waves {
			mapped.Waves[i] = waveView(wave)
		}
	}
	if value.Series != nil {
		mapped.Series = make([]domain.Series, len(value.Series))
		for i, series := range value.Series {
			mapped.Series[i] = adminCloneSeries(series)
		}
	}
	if value.PauseGraph != nil {
		mapped.PauseGraph = pauseGraphView(*value.PauseGraph)
	}
	return mapped
}

func pauseGraphView(value PauseGraphView) *inbound.AdminPauseGraphView {
	graph := value.Graph
	mapped := &inbound.AdminPauseGraphView{TournamentID: graph.Scope.TournamentID, RosterID: graph.Scope.RosterID, Revision: graph.Revision, Wave: waveView(value.Wave), ActivePauseID: nil, PausedAt: cloneTime(graph.PausedAt), DeadlinesSuppressed: graph.DeadlinesSuppressed, TerminalActionRevision: graph.TerminalActionRevision}
	if graph.ActivePauseID != uuid.Nil {
		mapped.ActivePauseID = cloneUUID(&graph.ActivePauseID)
	}
	if graph.Series != nil {
		mapped.Series = make([]inbound.AdminPauseSeriesView, len(graph.Series))
		for i, item := range graph.Series {
			mapped.Series[i] = inbound.AdminPauseSeriesView{Series: adminCloneSeries(item.Execution.Series), Revision: item.Revision, CurrentGameID: cloneUUID(item.CurrentGameID), ResumeState: copySeriesState(item.Execution.ResumeState)}
		}
	}
	if graph.Games != nil {
		mapped.Games = make([]inbound.AdminPauseGameView, len(graph.Games))
		for i, item := range graph.Games {
			mapped.Games[i] = inbound.AdminPauseGameView{SeriesID: item.SeriesID, Game: cloneGame(item.Game), Revision: item.Revision, Deadline: cloneTime(item.Deadline), ResumeState: copyGameState(item.ResumeState)}
		}
	}
	mapped.Draft = inboundDraftView(graph.Draft)
	if graph.Presence != nil {
		mapped.Presence = make([]inbound.AdminPresenceView, len(graph.Presence))
		for i, item := range graph.Presence {
			mapped.Presence[i] = inbound.AdminPresenceView{ID: item.ID, TournamentID: item.TournamentID, RosterID: item.RosterID, SeriesID: item.SeriesID, ParticipantID: item.ParticipantID, State: string(item.State), PresenceEpoch: item.PresenceEpoch, Revision: item.Revision, ConnectedAt: item.ConnectedAt, DisconnectedAt: cloneTime(item.DisconnectedAt), UpdatedAt: item.UpdatedAt}
		}
	}
	if graph.Reconnect != nil {
		mapped.Reconnect = make([]inbound.AdminReconnectIntervalView, len(graph.Reconnect))
		for i, item := range graph.Reconnect {
			mapped.Reconnect[i] = inbound.AdminReconnectIntervalView{ID: item.ID, PauseID: item.PauseID, RosterID: item.RosterID, SeriesID: item.SeriesID, GameID: item.GameID, ParticipantID: item.ParticipantID, PresenceEpoch: item.PresenceEpoch, Number: item.Number, ContinuationNumber: item.ContinuationNumber, ContinuedFromID: cloneUUID(item.ContinuedFromID), SuspendedByPauseID: cloneUUID(item.SuspendedByPauseID), State: string(item.State), OpenedAt: item.OpenedAt, Deadline: item.Deadline, ClosedAt: cloneTime(item.ClosedAt), Revision: item.Revision, UpdatedAt: item.UpdatedAt}
		}
	}
	if graph.Counters != nil {
		mapped.Counters = make([]inbound.AdminReconnectCounterView, len(graph.Counters))
		for i, item := range graph.Counters {
			mapped.Counters[i] = inbound.AdminReconnectCounterView{PauseID: item.PauseID, RosterID: item.RosterID, ParticipantID: item.ParticipantID, Limit: item.Limit, Used: item.Used, Revision: item.Revision}
		}
	}
	if graph.FrozenDeadlines != nil {
		mapped.FrozenDeadlines = make([]inbound.AdminFrozenDeadlineView, len(graph.FrozenDeadlines))
		for i, item := range graph.FrozenDeadlines {
			mapped.FrozenDeadlines[i] = inbound.AdminFrozenDeadlineView{Kind: string(item.Kind), OwnerID: item.OwnerID, OriginalDeadline: item.OriginalDeadline, FrozenAt: item.FrozenAt, Remaining: item.Remaining, ResumedAt: cloneTime(item.ResumedAt), ResumedDeadline: cloneTime(item.ResumedDeadline), Revision: item.Revision}
		}
	}
	return mapped
}

func inboundDraftView(value *draftusecase.Execution) *inbound.AdminDraftView {
	if value == nil {
		return nil
	}
	mapped := &inbound.AdminDraftView{ID: value.ID, SeriesID: value.SeriesID, Format: value.Format, FirstParticipantID: value.FirstParticipantID, SecondParticipantID: value.SecondParticipantID, Pool: append([]domain.Category(nil), value.Pool...), State: draftState(value.State), Turn: value.Turn, TurnDeadline: copyDraftDeadline(value.TurnDeadline), SelectedCategories: append([]domain.Category(nil), value.SelectedCategories...), Revision: value.Revision}
	if value.Actions != nil {
		mapped.Actions = make([]inbound.AdminDraftActionView, len(value.Actions))
		for i, action := range value.Actions {
			mapped.Actions[i] = inbound.AdminDraftActionView{Turn: action.Turn, ActorID: action.ActorID, Action: action.Action, Category: action.Category, OccurredAt: action.OccurredAt, TurnDeadline: action.ScheduledDeadline}
		}
	}
	return mapped
}
func copyDraftDeadline(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return cloneTime(&value)
}
func draftState(value draftusecase.ExecutionState) domain.DraftState {
	if value == draftusecase.ExecutionStateCompleted {
		return domain.DraftStateCompleted
	}
	return domain.DraftStateActive
}
func copySeriesState(value *domain.SeriesState) *domain.SeriesState {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
func copyGameState(value *domain.GameState) *domain.GameState {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
func adminCloneSeries(value domain.Series) domain.Series {
	cloned := value
	cloned.WinnerID = cloneUUID(value.WinnerID)
	cloned.Slots = append([]domain.GameSlot(nil), value.Slots...)
	for i := range cloned.Slots {
		cloned.Slots[i].Attempts = append([]domain.Game(nil), value.Slots[i].Attempts...)
		for j := range cloned.Slots[i].Attempts {
			cloned.Slots[i].Attempts[j] = cloneGame(value.Slots[i].Attempts[j])
		}
	}
	return cloned
}
func cloneGame(value domain.Game) domain.Game {
	cloned := value
	cloned.WinnerID = cloneUUID(value.WinnerID)
	return cloned
}
