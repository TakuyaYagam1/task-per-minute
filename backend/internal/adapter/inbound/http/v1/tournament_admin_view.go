package v1

import (
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func tournamentRosterResponse(view inbound.AdminRosterView) (api.Roster, error) {
	participants := make([]api.Participant, len(view.Participants))
	for index, participant := range view.Participants {
		if !fitsAPIInt32(participant.Seed) {
			return api.Roster{}, domain.ErrInternal
		}
		attendance := api.AttendanceState(participant.Attendance)
		if !attendance.Valid() {
			return api.Roster{}, domain.ErrInternal
		}
		participants[index] = api.Participant{
			Id: participant.ID, RosterId: participant.RosterID, TournamentId: participant.TournamentID,
			PlayerId: participant.PlayerID, Seed: response.IntToInt32(participant.Seed), Attendance: attendance,
			CreatedAt: participant.CreatedAt, UpdatedAt: participant.UpdatedAt,
		}
	}
	return api.Roster{
		Id: view.ID, TournamentId: view.TournamentID, Revision: view.Revision,
		Participants: participants, Locked: view.Locked, ExecutionStarted: view.ExecutionStarted,
		LockedAt: cloneTimePointer(view.LockedAt), ExecutionStartedAt: cloneTimePointer(view.ExecutionStartedAt),
		CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt,
	}, nil
}

func tournamentPreflightResponse(report inbound.AdminPreflightReport) (api.PreflightReport, error) {
	checks := make([]api.PreflightCheck, len(report.Checks))
	for index, check := range report.Checks {
		code := api.PreflightCode(check.Code)
		if !code.Valid() {
			return api.PreflightReport{}, domain.ErrInternal
		}
		checks[index] = api.PreflightCheck{
			Code: code, Passed: check.Passed, Explanation: check.Explanation,
			Evidence: append([]string(nil), check.Evidence...),
		}
	}
	revisions := make([]api.PreflightSourceRevision, len(report.Revisions))
	for index, revision := range report.Revisions {
		revisions[index] = api.PreflightSourceRevision{Source: revision.Source, Value: revision.Value}
	}
	return api.PreflightReport{
		Id: report.ID, TournamentId: report.TournamentID,
		AlgorithmVersion: api.PreflightReportAlgorithmVersion(report.AlgorithmVersion),
		EvaluatedAt:      report.EvaluatedAt, NormalizedInputs: append([]string(nil), report.NormalizedInputs...),
		Revisions: revisions, ProofHash: report.ProofHash, Checks: checks, Passed: adminPreflightPassed(report.Checks),
	}, nil
}

func adminPreflightPassed(checks []inbound.AdminPreflightCheck) bool {
	if len(checks) == 0 {
		return false
	}
	for _, check := range checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

func tournamentSwissRoundResponse(view inbound.AdminSwissRoundView) (api.SwissRound, error) {
	if !fitsAPIInt32(view.RoundNumber) {
		return api.SwissRound{}, domain.ErrInternal
	}
	pairings := make([]api.SwissPairing, len(view.Pairings))
	for index, pairing := range view.Pairings {
		repeated := pairing.Repeated
		pairings[index] = api.SwissPairing{
			Id: pairing.ID, RoundId: pairing.RoundID,
			FirstParticipantId: pairing.FirstParticipantID, SecondParticipantId: pairing.SecondParticipantID,
			EvidenceId: pairing.EvidenceID, Repeated: &repeated,
			OverrideActorId: cloneUUIDPointer(pairing.OverrideActorID),
			OverrideReason:  cloneStringPointer(pairing.OverrideReason),
		}
	}
	standings := make([]api.SwissStanding, len(view.Standings))
	for index, standing := range view.Standings {
		if !fitsAPIInt32(standing.Position) || !fitsAPIInt32(standing.Points) ||
			!fitsAPIInt32(standing.Buchholz) || !fitsAPIInt32(standing.HeadToHeadPoints) ||
			!fitsAPIInt32(standing.StableSeed) {
			return api.SwissRound{}, domain.ErrInternal
		}
		pointsLabel := api.SwissPointsLabel(standing.PointsLabel)
		buchholzStatus := api.SwissBuchholzStatus(standing.BuchholzStatus)
		if !pointsLabel.Valid() || !buchholzStatus.Valid() {
			return api.SwissRound{}, domain.ErrInternal
		}
		standings[index] = api.SwissStanding{
			ParticipantId: standing.ParticipantID, Position: response.IntToInt32(standing.Position),
			Points: response.IntToInt32(standing.Points), PointsLabel: pointsLabel,
			Buchholz: response.IntToInt32(standing.Buchholz), BuchholzStatus: buchholzStatus,
			HeadToHeadPoints: response.IntToInt32(standing.HeadToHeadPoints), HeadToHeadApplied: standing.HeadToHeadApplied,
			EffectiveTimeMs:     standing.EffectiveTimeMS,
			AcceptedSolveTimeMs: cloneInt64Pointer(standing.AcceptedSolveTimeMS),
			StableSeed:          response.IntToInt32(standing.StableSeed),
		}
	}
	pairingEvidence, err := tournamentSwissPairingEvidenceResponse(view.PairingEvidence)
	if err != nil {
		return api.SwissRound{}, err
	}
	bye, err := tournamentSwissByeResponse(view.Bye)
	if err != nil {
		return api.SwissRound{}, err
	}
	return api.SwissRound{
		Id: view.ID, TournamentId: view.TournamentID, RoundNumber: response.IntToInt32(view.RoundNumber),
		Revision: view.Revision, RosterParticipantIds: append([]uuid.UUID(nil), view.RosterParticipantIDs...),
		PairingEvidence: pairingEvidence,
		Pairings:        pairings, Bye: bye, Standings: standings,
		Locked: view.Locked, LockedAt: cloneTimePointer(view.LockedAt), StartedAt: cloneTimePointer(view.StartedAt),
		CompletedAt: cloneTimePointer(view.CompletedAt), CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt,
	}, nil
}

func tournamentSwissPairingEvidenceResponse(
	view *inbound.AdminSwissPairingEvidenceView,
) (*api.SwissPairingEvidence, error) {
	if view == nil {
		return nil, nil
	}
	purpose := api.SwissPairingEvidencePurpose(view.Purpose)
	algorithm := api.SwissPairingEvidenceAlgorithmVersion(view.AlgorithmVersion)
	if !purpose.Valid() || !algorithm.Valid() {
		return nil, domain.ErrInternal
	}
	return &api.SwissPairingEvidence{
		Id: view.ID, Purpose: purpose,
		AlgorithmVersion: algorithm,
		NormalizedInputs: append([]string(nil), view.NormalizedInputs...),
		Result:           append([]string(nil), view.Result...), ReplayDigest: view.ReplayDigest,
		OwnerId: view.OwnerID, DecidedAt: view.DecidedAt,
	}, nil
}

func tournamentSwissByeResponse(view *inbound.AdminSwissByeView) (*api.SwissBye, error) {
	if view == nil {
		return nil, nil
	}
	if !fitsAPIInt32(view.PointsAwarded) {
		return nil, domain.ErrInternal
	}
	return &api.SwissBye{
		Id: view.ID, RoundId: view.RoundID, ParticipantId: view.ParticipantID,
		PointsAwarded: response.IntToInt32(view.PointsAwarded), RevisionId: view.RevisionID, EvidenceId: view.EvidenceID,
	}, nil
}

func tournamentWaveResponse(view inbound.AdminWaveView) (api.Wave, error) {
	wave := view.Wave
	state := api.WaveState(wave.State)
	if !state.Valid() {
		return api.Wave{}, domain.ErrInternal
	}
	members := make([]api.WaveMember, len(wave.Members))
	for index, member := range wave.Members {
		revision := view.ReadinessRevisions[member.ParticipantID]
		seriesID, assigned := view.SeriesIDs[member.ParticipantID]
		if revision < 1 || assigned && seriesID == uuid.Nil {
			return api.Wave{}, domain.ErrInternal
		}
		var seriesIDValue *uuid.UUID
		if assigned {
			seriesIDValue = &seriesID
		} else if view.ByeParticipantID == nil || *view.ByeParticipantID != member.ParticipantID {
			return api.Wave{}, domain.ErrInternal
		}
		members[index] = api.WaveMember{
			ParticipantId: member.ParticipantID, SeriesId: seriesIDValue,
			Ready: member.Ready, ReadinessRevision: revision,
		}
	}
	var readyWindow *api.ReadyWindow
	if wave.ReadyWindow != nil {
		windowState := api.ReadyWindowState(wave.ReadyWindow.State)
		if !windowState.Valid() {
			return api.Wave{}, domain.ErrInternal
		}
		readyWindow = &api.ReadyWindow{
			Id: wave.ReadyWindow.ID, WaveId: wave.ReadyWindow.WaveID,
			RevisionId: wave.ReadyWindow.RevisionID.UUID(), State: windowState,
			OpenedAt: wave.ReadyWindow.OpenedAt, Deadline: wave.ReadyWindow.Deadline,
			ConsumedAt: cloneTimePointer(wave.ReadyWindow.ConsumedAt),
		}
	}
	return api.Wave{
		Id: wave.ID, TournamentId: wave.TournamentID, RevisionId: wave.RevisionID.UUID(),
		Revision: view.Revision, State: state, Members: members, ReadyWindow: readyWindow,
		StartedAt: cloneTimePointer(wave.StartedAt), PausedAt: cloneTimePointer(wave.PausedAt),
	}, nil
}

func tournamentCorrectionResponse(
	evidence inbound.AdminCorrectionEvidence,
) (api.CorrectionEvidence, error) {
	supersessions := make([]api.CorrectionProjectionSupersession, len(evidence.Supersessions))
	for index, item := range evidence.Supersessions {
		artifactKind := api.ArtifactKind(item.ArtifactKind)
		if !artifactKind.Valid() {
			return api.CorrectionEvidence{}, domain.ErrInternal
		}
		supersessions[index] = api.CorrectionProjectionSupersession{
			ArtifactKind: artifactKind, ArtifactId: item.ArtifactID,
			PreviousRevisionId: item.PreviousRevisionID, SuccessorRevisionId: item.SuccessorRevisionID,
			PreviousDecisionId:    cloneUUIDPointer(item.PreviousDecisionID),
			ReplacementDecisionId: item.ReplacementDecisionID,
		}
	}
	unlocks := make([]api.CorrectionUnlockIntent, len(evidence.UnlockIntents))
	for index, intent := range evidence.UnlockIntents {
		unlocks[index] = tournamentCorrectionUnlockIntentResponse(intent)
	}
	fields := make([]api.CorrectionField, len(evidence.Fields))
	for index, field := range evidence.Fields {
		mapped := api.CorrectionField(field)
		if !mapped.Valid() {
			return api.CorrectionEvidence{}, domain.ErrInternal
		}
		fields[index] = mapped
	}
	reason := api.CorrectionReason(evidence.Reason)
	if !reason.Valid() {
		return api.CorrectionEvidence{}, domain.ErrInternal
	}
	return api.CorrectionEvidence{
		CommandId: evidence.CommandID, TournamentId: evidence.TournamentID,
		SeriesId: evidence.SeriesID, GameId: evidence.GameID, OperatorId: evidence.OperatorID,
		Reason: reason, Fields: fields, RequestedAt: evidence.RequestedAt,
		ValidationDigest: hex.EncodeToString(evidence.ValidationDigest[:]),
		Supersessions:    supersessions, UnlockIntents: unlocks,
	}, nil
}

func tournamentCorrectionUnlockIntentResponse(
	intent inbound.AdminCorrectionUnlockIntent,
) api.CorrectionUnlockIntent {
	return api.CorrectionUnlockIntent{
		ReservationId: intent.ReservationID, TournamentId: intent.TournamentID, OwnerId: intent.OwnerID,
		SourceRevisionId: intent.SourceRevisionID, ExpectedRevision: intent.ExpectedRevision,
		ExpectedUsed: intent.ExpectedUsed, ExpectedDisclosed: intent.ExpectedDisclosed,
		EvidenceDigest: hex.EncodeToString(intent.EvidenceDigest[:]),
		BindingDigest:  hex.EncodeToString(intent.BindingDigest[:]),
	}
}

func tournamentAuditPageResponse(page inbound.AdminAuditPage) (api.AuditPage, error) {
	events := make([]api.AuditEvent, len(page.Events))
	for index, event := range page.Events {
		var payload map[string]interface{}
		if err := json.Unmarshal(event.RedactedPayload, &payload); err != nil || payload == nil {
			return api.AuditPage{}, domain.ErrInternal
		}
		events[index] = api.AuditEvent{
			AuditEventId: event.AuditEventID, TournamentId: event.TournamentID,
			RosterId: event.RosterID, SeriesId: event.SeriesID, ResultEventId: event.ResultEventID,
			ActorKind: api.ResultActorKind(event.ActorKind), ActorId: cloneUUIDPointer(event.ActorID),
			EventType: event.EventType, RedactedPayload: payload, OccurredAt: event.OccurredAt,
			CreatedAt: event.CreatedAt, ResultState: event.ResultState, ResultReason: event.ResultReason,
			WinnerId: cloneUUIDPointer(event.WinnerID), OfficialResultRevisionId: event.OfficialResultRevisionID,
			EntityKind: api.AuditEntityKind(event.EntityKind), EntityId: event.EntityID,
			RevisionNumber: event.RevisionNumber, IsCurrent: event.IsCurrent, IsSuperseded: event.IsSuperseded,
		}
	}
	var cursor *api.AuditCursor
	if page.NextCursor != nil {
		cursor = &api.AuditCursor{
			OccurredAt: page.NextCursor.OccurredAt, AuditEventId: page.NextCursor.AuditEventID,
			RevisionId: page.NextCursor.RevisionID, SnapshotBound: page.NextCursor.SnapshotBound,
		}
	}
	return api.AuditPage{Events: events, NextCursor: cursor}, nil
}

func tournamentIncidentResponse(bundle inbound.AdminIncidentBundle) api.IncidentBundle {
	return api.IncidentBundle{
		TournamentId: bundle.TournamentID, ProjectionRevision: bundle.ProjectionRevision,
		GeneratedAt: bundle.GeneratedAt, CanonicalContent: append([]byte(nil), bundle.CanonicalContent...),
		CanonicalContentType:     api.IncidentBundleCanonicalContentType("application/json"),
		CanonicalContentEncoding: api.IncidentBundleCanonicalContentEncoding("base64"),
		CanonicalContentLength:   int64(len(bundle.CanonicalContent)), Sha256: hex.EncodeToString(bundle.SHA256[:]),
		Algorithm: api.IncidentBundleAlgorithm(bundle.Algorithm), KeyId: bundle.KeyID,
		Mac: hex.EncodeToString(bundle.MAC[:]),
	}
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func fitsAPIInt32(value int) bool {
	return value >= math.MinInt32 && value <= math.MaxInt32
}
