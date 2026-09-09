package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func loadTournamentAdminCorrectionGoldenStage(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	tournamentRevision int64,
) (correctionusecase.StageLayout, error) {
	params := sqlc.LockCorrectionGoldenStageGroupsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		TournamentRevision: tournamentRevision,
	}
	groups, err := querier.LockCorrectionGoldenStageGroups(ctx, params)
	if err != nil {
		return correctionusecase.StageLayout{}, tournamentAdminCorrectionError("lock Golden stage groups", err)
	}
	if len(groups) == 0 {
		return correctionusecase.StageLayout{}, domain.ErrConflict
	}
	stageCommandID := groups[0].StageProgressionCommandID
	for _, group := range groups {
		if group.StageProgressionCommandID != stageCommandID {
			return correctionusecase.StageLayout{}, domain.ErrConflict
		}
	}
	queryScope := sqlc.LockCorrectionGoldenStageMembersParams{
		StageProgressionCommandID: stageCommandID,
		TournamentID:              scope.TournamentID,
		RosterID:                  scope.RosterID,
	}
	tieMembers, err := querier.LockCorrectionGoldenStageMembers(ctx, queryScope)
	if err != nil {
		return correctionusecase.StageLayout{}, tournamentAdminCorrectionError("lock Golden stage members", err)
	}
	stateMembers, err := querier.LockCorrectionGoldenLatestStateMembers(
		ctx,
		sqlc.LockCorrectionGoldenLatestStateMembersParams(queryScope),
	)
	if err != nil {
		return correctionusecase.StageLayout{}, tournamentAdminCorrectionError("lock latest Golden state members", err)
	}
	attempts, err := querier.LockCorrectionGoldenLatestStateAttempts(
		ctx,
		sqlc.LockCorrectionGoldenLatestStateAttemptsParams(queryScope),
	)
	if err != nil {
		return correctionusecase.StageLayout{}, tournamentAdminCorrectionError("lock latest Golden state attempts", err)
	}
	attemptMembers, err := querier.LockCorrectionGoldenLatestStateAttemptMembers(
		ctx,
		sqlc.LockCorrectionGoldenLatestStateAttemptMembersParams(queryScope),
	)
	if err != nil {
		return correctionusecase.StageLayout{}, tournamentAdminCorrectionError("lock latest Golden attempt members", err)
	}
	prestart, err := querier.LockCorrectionGoldenPrestartHeads(
		ctx,
		sqlc.LockCorrectionGoldenPrestartHeadsParams(queryScope),
	)
	if err != nil {
		return correctionusecase.StageLayout{}, tournamentAdminCorrectionError("lock Golden prestart heads", err)
	}
	return mapTournamentAdminCorrectionGoldenStage(
		scope.TournamentID, groups, tieMembers, stateMembers, attempts, attemptMembers, prestart,
	)
}

func mapTournamentAdminCorrectionGoldenStage(
	tournamentID uuid.UUID,
	rows []sqlc.LockCorrectionGoldenStageGroupsRow,
	tieMembers []sqlc.LockCorrectionGoldenStageMembersRow,
	stateMembers []sqlc.LockCorrectionGoldenLatestStateMembersRow,
	attemptRows []sqlc.LockCorrectionGoldenLatestStateAttemptsRow,
	attemptMemberRows []sqlc.LockCorrectionGoldenLatestStateAttemptMembersRow,
	prestartRows []sqlc.LockCorrectionGoldenPrestartHeadsRow,
) (correctionusecase.StageLayout, error) {
	memberByRevision := make(map[uuid.UUID][]domain.GoldenMember, len(rows))
	for _, row := range tieMembers {
		if row.GroupID == uuid.Nil || row.GroupRevisionID == uuid.Nil || row.ParticipantID == uuid.Nil || row.StandingPosition < 1 {
			return correctionusecase.StageLayout{}, domain.ErrConflict
		}
		memberByRevision[row.GroupRevisionID] = append(memberByRevision[row.GroupRevisionID], domain.GoldenMember{
			ParticipantID: row.ParticipantID,
		})
	}
	stateMemberByRevision := make(map[uuid.UUID][]domain.GoldenMember, len(rows))
	for _, row := range stateMembers {
		if row.GroupID == uuid.Nil || row.GroupRevisionID == uuid.Nil || row.StateRevisionID == uuid.Nil ||
			row.ParticipantID == uuid.Nil || row.Position < 1 {
			return correctionusecase.StageLayout{}, domain.ErrConflict
		}
		stateMemberByRevision[row.GroupRevisionID] = append(
			stateMemberByRevision[row.GroupRevisionID],
			domain.GoldenMember{ParticipantID: row.ParticipantID, Excluded: row.Excluded},
		)
	}
	attemptMembersByID := make(map[uuid.UUID][]uuid.UUID, len(attemptRows))
	for _, row := range attemptMemberRows {
		if row.GroupRevisionID == uuid.Nil || row.StateRevisionID == uuid.Nil || row.AttemptID == uuid.Nil ||
			row.ParticipantID == uuid.Nil || row.Position < 1 {
			return correctionusecase.StageLayout{}, domain.ErrConflict
		}
		attemptMembersByID[row.AttemptID] = append(attemptMembersByID[row.AttemptID], row.ParticipantID)
	}
	attemptsByRevision := make(map[uuid.UUID][]domain.GoldenAttempt, len(rows))
	for _, row := range attemptRows {
		attempt, mapErr := mapTournamentAdminCorrectionGoldenAttempt(row, attemptMembersByID[row.AttemptID])
		if mapErr != nil {
			return correctionusecase.StageLayout{}, mapErr
		}
		attemptsByRevision[row.GroupRevisionID] = append(attemptsByRevision[row.GroupRevisionID], attempt)
	}
	layout := correctionusecase.StageLayout{
		Mode:         correctionusecase.StageModeGolden,
		GoldenGroups: make([]domain.GoldenGroupState, 0, len(rows)),
	}
	knownGroups := make(map[uuid.UUID]domain.DerivedRevisionID, len(rows))
	for _, row := range rows {
		if row.GroupID == uuid.Nil || row.GroupRevisionID == uuid.Nil || row.SourceProjectionRevisionID == uuid.Nil ||
			row.SourceProjectionRevision < 1 || row.PositionFrom < 1 || row.PositionTo < row.PositionFrom ||
			len(row.DefinitionDigest) != sha256.Size {
			return correctionusecase.StageLayout{}, domain.ErrConflict
		}
		members := memberByRevision[row.GroupRevisionID]
		if current, found := stateMemberByRevision[row.GroupRevisionID]; found {
			members = current
		}
		attempts := attemptsByRevision[row.GroupRevisionID]
		group := domain.GoldenGroupState{
			ID: row.GroupID, TournamentID: tournamentID,
			RevisionID:                 domain.DerivedRevisionID(row.GroupRevisionID),
			SourceProjectionRevisionID: domain.DerivedRevisionID(row.SourceProjectionRevisionID),
			PositionFrom:               int(row.PositionFrom), PositionTo: int(row.PositionTo),
			ParticipationEstablished: correctionGoldenParticipationEstablished(attempts),
			Members:                  members, Attempts: attempts,
		}
		if _, err := domain.NewGoldenGroup(group); err != nil {
			return correctionusecase.StageLayout{}, fmt.Errorf("map Golden correction group: %v: %w", err, domain.ErrConflict)
		}
		layout.GoldenGroups = append(layout.GoldenGroups, group)
		knownGroups[row.GroupID] = group.RevisionID
	}
	for _, row := range prestartRows {
		if !row.GroupID.Valid || !row.GroupRevisionID.Valid || row.RepositoryScopeID == uuid.Nil ||
			row.RevisionID == uuid.Nil || row.RevisionNumber < 1 || len(row.PayloadDigest) != sha256.Size ||
			knownGroups[row.GroupID.UUID] != domain.DerivedRevisionID(row.GroupRevisionID.UUID) {
			return correctionusecase.StageLayout{}, domain.ErrConflict
		}
		sessionID, state, parseErr := correctionGoldenPrestartHead(row.Payload)
		if parseErr != nil {
			return correctionusecase.StageLayout{}, parseErr
		}
		if state != correctionusecase.StagePauseStatePaused {
			continue
		}
		digest, ok := correctionDigestFromBytes(row.PayloadDigest)
		if !ok {
			return correctionusecase.StageLayout{}, domain.ErrConflict
		}
		layout.Paused = append(layout.Paused, correctionusecase.StagePauseExpectation{
			TournamentID:    tournamentID,
			GroupID:         row.GroupID.UUID,
			GroupRevisionID: domain.DerivedRevisionID(row.GroupRevisionID.UUID),
			SessionID:       sessionID, RevisionID: row.RevisionID, Revision: row.RevisionNumber,
			State: state, PayloadDigest: digest,
		})
	}
	return layout, nil
}

func mapTournamentAdminCorrectionGoldenAttempt(
	row sqlc.LockCorrectionGoldenLatestStateAttemptsRow,
	participantIDs []uuid.UUID,
) (domain.GoldenAttempt, error) {
	if row.GroupID == uuid.Nil || row.GroupRevisionID == uuid.Nil || row.StateRevisionID == uuid.Nil ||
		row.StateRevision < 1 || len(row.StatePayloadDigest) != sha256.Size || row.AttemptID == uuid.Nil ||
		row.AttemptNumber < 1 || len(participantIDs) < 2 ||
		!correctionGoldenAttemptStateMatches(row.SnapshotState, row.AttemptState) {
		return domain.GoldenAttempt{}, domain.ErrConflict
	}
	attempt := domain.GoldenAttempt{
		ID: row.AttemptID, GroupID: row.GroupID,
		GroupRevisionID: domain.DerivedRevisionID(row.GroupRevisionID), AttemptNo: int(row.AttemptNumber),
		State: domain.GoldenAttemptState(row.SnapshotState), ParticipantIDs: append([]uuid.UUID(nil), participantIDs...),
		RetainedAt: correctionGoldenTimePointer(row.RetainedAt),
		StartedAt:  correctionGoldenTimePointer(row.SnapshotStartedAt),
		FinishedAt: correctionGoldenTimePointer(row.SnapshotFinishedAt),
	}
	if row.PreviousAttemptID.Valid {
		previous := row.PreviousAttemptID.UUID
		attempt.PreviousAttemptID = &previous
	}
	if err := attempt.Validate(); err != nil {
		return domain.GoldenAttempt{}, domain.ErrConflict
	}
	return attempt, nil
}

func correctionGoldenAttemptStateMatches(snapshot, physical string) bool {
	switch domain.GoldenAttemptState(snapshot) {
	case domain.GoldenAttemptStatePlanned:
		return physical == "prepared"
	case domain.GoldenAttemptStateWaitingReady:
		return physical == "ready"
	case domain.GoldenAttemptStateActive:
		return physical == "active" || physical == "technical_pause"
	case domain.GoldenAttemptStateCompleted:
		return physical == "completed"
	case domain.GoldenAttemptStateVoid:
		return physical == "superseded"
	case domain.GoldenAttemptStateCancelled:
		return physical == "cancelled"
	default:
		return false
	}
}

func correctionGoldenTimePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func correctionGoldenParticipationEstablished(attempts []domain.GoldenAttempt) bool {
	for _, attempt := range attempts {
		if attempt.StartedAt != nil || attempt.State == domain.GoldenAttemptStateActive ||
			attempt.State == domain.GoldenAttemptStateCompleted || attempt.State == domain.GoldenAttemptStateVoid {
			return true
		}
	}
	return false
}

func correctionGoldenPrestartHead(payload []byte) (uuid.UUID, correctionusecase.StagePauseState, error) {
	var envelope struct {
		Document json.RawMessage `json:"document"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &envelope) != nil || len(envelope.Document) == 0 {
		return uuid.Nil, "", domain.ErrConflict
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(envelope.Document, &document) != nil {
		return uuid.Nil, "", domain.ErrConflict
	}
	session, ok := document["session_id"]
	if !ok {
		session = document["SessionID"]
	}
	stateValue, ok := document["state"]
	if !ok {
		stateValue = document["State"]
	}
	var sessionID uuid.UUID
	var state string
	if len(session) == 0 || len(stateValue) == 0 || json.Unmarshal(session, &sessionID) != nil ||
		json.Unmarshal(stateValue, &state) != nil || sessionID == uuid.Nil {
		return uuid.Nil, "", domain.ErrConflict
	}
	return sessionID, correctionusecase.StagePauseState(state), nil
}

func lockTournamentAdminCorrectionStage(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	scope ResultScope,
) (correctionusecase.StageSnapshot, sqlc.LockCorrectionTournamentScopeRow, error) {
	tournament, err := querier.LockCorrectionTournamentScope(ctx, sqlc.LockCorrectionTournamentScopeParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return correctionusecase.StageSnapshot{}, sqlc.LockCorrectionTournamentScopeRow{}, tournamentAdminCorrectionError("relock correction stage", err)
	}
	if tournament.TournamentState != string(mutation.Authority.Stage.TournamentState) ||
		tournament.TournamentRevision != mutation.Authority.Stage.TournamentRevision {
		return correctionusecase.StageSnapshot{}, sqlc.LockCorrectionTournamentScopeRow{}, domain.ErrConflict
	}
	stage, err := loadTournamentAdminCorrectionStage(ctx, querier, correctionAuthorityInputs{
		tournamentState: domain.TournamentState(tournament.TournamentState), tournamentVersion: tournament.TournamentRevision,
	}, scope)
	if err != nil {
		return correctionusecase.StageSnapshot{}, sqlc.LockCorrectionTournamentScopeRow{}, err
	}
	if err := correctionusecase.ValidateServerOwnedStageRollback(
		mutation.Plan, stage, mutation.Evidence.RequestedAt, mutation.Stage,
	); err != nil {
		return correctionusecase.StageSnapshot{}, sqlc.LockCorrectionTournamentScopeRow{}, err
	}
	return stage, tournament, nil
}

func persistTournamentAdminCorrectionStage(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	input CorrectionInput,
	snapshot correctionusecase.StageSnapshot,
	tournament sqlc.LockCorrectionTournamentScopeRow,
) error {
	if mutation.Stage.Transition == correctionusecase.StageUnchanged {
		return nil
	}
	action, resultingState, err := correctionStagePersistenceTransition(mutation.Stage)
	if err != nil {
		return err
	}
	goldenAuthority, err := lockTournamentAdminCorrectionGoldenTombstoneAuthority(
		ctx, querier, mutation, snapshot,
	)
	if err != nil {
		return err
	}
	changedAt := mutation.Evidence.RequestedAt
	resultingRevision := snapshot.TournamentRevision + 1
	advanced, err := querier.AdvanceCorrectionTournamentStageCAS(ctx, sqlc.AdvanceCorrectionTournamentStageCASParams{
		ResultingTournamentState: string(resultingState), UpdatedAt: tstz(changedAt),
		TournamentID: snapshot.TournamentID, ExpectedTournamentState: string(snapshot.TournamentState),
		ExpectedTournamentRevision: snapshot.TournamentRevision,
	})
	if err != nil {
		return correctionStageWriteError("advance Tournament stage", err)
	}
	if advanced.ID != snapshot.TournamentID || advanced.State != string(resultingState) || advanced.Revision != resultingRevision {
		return domain.ErrConflict
	}
	commandID, err := querier.CreateCorrectionTournamentLifecycleCommand(ctx, sqlc.CreateCorrectionTournamentLifecycleCommandParams{
		CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
		ActorID: mutation.Command.Operator.ActorID, Action: action,
		SourceProjectionRevisionID: mutation.Authority.ProjectionRevisionID,
		SourceProjectionRevision:   mutation.Authority.ProjectionRevision,
		SourceTournamentRevision:   snapshot.TournamentRevision, SourceTournamentState: string(snapshot.TournamentState),
		ResultingTournamentRevision: resultingRevision, ResultingTournamentState: string(resultingState),
		Preset: tournament.TournamentPreset, RosterSize: tournament.RosterSize,
		TournamentCreatedAt: tournament.TournamentCreatedAt, TournamentUpdatedAt: tstz(changedAt),
		TournamentStartedAt: tournament.TournamentStartedAt, TournamentFinishedAt: tournament.TournamentFinishedAt,
		ExecutedAt: tstz(changedAt), CreatedAt: tstz(changedAt),
	})
	if err := correctionStageWrittenID("create lifecycle command", mutation.Command.CommandID, commandID, err); err != nil {
		return err
	}
	resultingProjectionRevision := mutation.Authority.ProjectionRevision + 1
	commandID, err = querier.CreateCorrectionStageProgression(ctx, sqlc.CreateCorrectionStageProgressionParams{
		CommandID: mutation.Command.CommandID, ActorID: mutation.Command.Operator.ActorID, Action: action,
		SourceTournamentRevision: snapshot.TournamentRevision, SourceTournamentState: string(snapshot.TournamentState),
		SourceProjectionRevisionID:    mutation.Authority.ProjectionRevisionID,
		SourceProjectionRevision:      mutation.Authority.ProjectionRevision,
		ResultingProjectionRevisionID: nullableUUIDValue(input.ProjectionIDs.RevisionID),
		ResultingProjectionRevision:   &resultingProjectionRevision,
		ResultingTournamentRevision:   resultingRevision, ResultingTournamentState: string(resultingState),
		Proof: mutation.Stage.Proof, ProofDigest: mutation.Stage.ProofDigest[:],
		ExecutedAt: tstz(changedAt), CreatedAt: tstz(changedAt),
		TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
	})
	if err := correctionStageWrittenID("create stage progression", mutation.Command.CommandID, commandID, err); err != nil {
		return err
	}
	if mutation.Stage.CreatePlayoff {
		if err := persistTournamentAdminCorrectionPlayoffStage(ctx, querier, mutation, input, changedAt); err != nil {
			return err
		}
	}
	if resultingState == domain.TournamentStateGolden {
		if err := persistTournamentAdminCorrectionGoldenGroups(ctx, querier, mutation, input, snapshot, changedAt); err != nil {
			return err
		}
	}
	planDigest := sha256.Sum256(mutation.Plan.Bytes())
	commandID, err = querier.CreateGoldenCorrectionStageTombstone(ctx, sqlc.CreateGoldenCorrectionStageTombstoneParams{
		CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
		SourceTournamentRevision: snapshot.TournamentRevision, ResultingTournamentRevision: resultingRevision,
		ResultingTournamentState:      string(resultingState),
		SourceProjectionRevisionID:    mutation.Authority.ProjectionRevisionID,
		SourceProjectionRevision:      mutation.Authority.ProjectionRevision,
		ResultingProjectionRevisionID: input.ProjectionIDs.RevisionID,
		ResultingProjectionRevision:   resultingProjectionRevision,
		ProofDigest:                   mutation.Stage.ProofDigest[:], CorrectedAt: tstz(changedAt), CreatedAt: tstz(changedAt),
	})
	if err := correctionStageWrittenID("create stage tombstone", mutation.Command.CommandID, commandID, err); err != nil {
		return err
	}
	if err := persistTournamentAdminCorrectionGoldenTombstones(
		ctx, querier, mutation, snapshot, goldenAuthority, changedAt,
	); err != nil {
		return err
	}
	commandID, err = querier.SealGoldenCorrectionTombstones(ctx, sqlc.SealGoldenCorrectionTombstonesParams{
		CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
		ProofDigest: planDigest[:], SealedAt: tstz(changedAt),
	})
	return correctionStageWrittenID("seal stage tombstones", mutation.Command.CommandID, commandID, err)
}

type correctionGoldenTombstoneAuthority struct {
	stageCommandID uuid.UUID
	groups         map[uuid.UUID]sqlc.LockCorrectionGoldenStageGroupsRow
	states         []sqlc.LockCorrectionGoldenStateRevisionsRow
	positions      []sqlc.LockCorrectionGoldenPositionLedgerRevisionsRow
	attempts       map[uuid.UUID]sqlc.LockCorrectionGoldenLatestStateAttemptsRow
}

func lockTournamentAdminCorrectionGoldenTombstoneAuthority(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	snapshot correctionusecase.StageSnapshot,
) (correctionGoldenTombstoneAuthority, error) {
	authority := correctionGoldenTombstoneAuthority{
		groups:   make(map[uuid.UUID]sqlc.LockCorrectionGoldenStageGroupsRow),
		attempts: make(map[uuid.UUID]sqlc.LockCorrectionGoldenLatestStateAttemptsRow),
	}
	if len(mutation.Stage.GroupSupersessions) == 0 {
		return authority, nil
	}
	if snapshot.Layout.Mode != correctionusecase.StageModeGolden {
		return correctionGoldenTombstoneAuthority{}, domain.ErrConflict
	}
	groups, err := querier.LockCorrectionGoldenStageGroups(ctx, sqlc.LockCorrectionGoldenStageGroupsParams{
		TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
		TournamentRevision: snapshot.TournamentRevision,
	})
	if err != nil {
		return correctionGoldenTombstoneAuthority{}, tournamentAdminCorrectionError("relock Golden tombstone groups", err)
	}
	if len(groups) == 0 {
		return correctionGoldenTombstoneAuthority{}, domain.ErrConflict
	}
	authority.stageCommandID = groups[0].StageProgressionCommandID
	for _, group := range groups {
		if group.StageProgressionCommandID != authority.stageCommandID || group.GroupRevisionID == uuid.Nil {
			return correctionGoldenTombstoneAuthority{}, domain.ErrConflict
		}
		if _, duplicate := authority.groups[group.GroupRevisionID]; duplicate {
			return correctionGoldenTombstoneAuthority{}, domain.ErrConflict
		}
		authority.groups[group.GroupRevisionID] = group
	}
	query := sqlc.LockCorrectionGoldenStateRevisionsParams{
		StageProgressionCommandID: authority.stageCommandID,
		TournamentID:              snapshot.TournamentID,
		RosterID:                  mutation.Authority.RosterID,
	}
	authority.states, err = querier.LockCorrectionGoldenStateRevisions(ctx, query)
	if err != nil {
		return correctionGoldenTombstoneAuthority{}, tournamentAdminCorrectionError("lock Golden state lineage", err)
	}
	authority.positions, err = querier.LockCorrectionGoldenPositionLedgerRevisions(
		ctx,
		sqlc.LockCorrectionGoldenPositionLedgerRevisionsParams(query),
	)
	if err != nil {
		return correctionGoldenTombstoneAuthority{}, tournamentAdminCorrectionError("lock Golden position lineage", err)
	}
	attempts, err := querier.LockCorrectionGoldenLatestStateAttempts(
		ctx,
		sqlc.LockCorrectionGoldenLatestStateAttemptsParams(query),
	)
	if err != nil {
		return correctionGoldenTombstoneAuthority{}, tournamentAdminCorrectionError("lock Golden retained attempts", err)
	}
	for _, attempt := range attempts {
		if attempt.AttemptID == uuid.Nil {
			return correctionGoldenTombstoneAuthority{}, domain.ErrConflict
		}
		if _, duplicate := authority.attempts[attempt.AttemptID]; duplicate {
			return correctionGoldenTombstoneAuthority{}, domain.ErrConflict
		}
		authority.attempts[attempt.AttemptID] = attempt
	}
	for _, supersession := range mutation.Stage.GroupSupersessions {
		group, found := authority.groups[supersession.PreviousRevisionID.UUID()]
		if !found || group.GroupID != supersession.GroupID || supersession.RevisionID.IsZero() {
			return correctionGoldenTombstoneAuthority{}, domain.ErrConflict
		}
	}
	return authority, nil
}

func persistTournamentAdminCorrectionGoldenTombstones(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	snapshot correctionusecase.StageSnapshot,
	authority correctionGoldenTombstoneAuthority,
	changedAt time.Time,
) error {
	if len(mutation.Stage.GroupSupersessions) == 0 {
		if len(mutation.Stage.CancelledAttempts) != 0 {
			return domain.ErrConflict
		}
		return nil
	}
	successors := make(map[uuid.UUID]domain.DerivedRevisionID, len(mutation.Stage.GroupSupersessions))
	affected := make(map[uuid.UUID]struct{}, len(mutation.Stage.GroupSupersessions))
	for _, supersession := range mutation.Stage.GroupSupersessions {
		previousID := supersession.PreviousRevisionID.UUID()
		replacement := uuid.NullUUID{}
		if supersession.ReplacementGroupID != nil {
			replacement = nullableUUID(supersession.ReplacementGroupID)
		}
		written, err := querier.CreateGoldenCorrectionGroupTombstone(ctx, sqlc.CreateGoldenCorrectionGroupTombstoneParams{
			CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID,
			RosterID: mutation.Authority.RosterID, GroupID: supersession.GroupID,
			GroupRevisionID: previousID, SuccessorRevisionID: supersession.RevisionID.UUID(),
			ReplacementGroupID: replacement, SupersededAt: tstz(changedAt),
			ProofDigest: mutation.Stage.ProofDigest[:], CreatedAt: tstz(changedAt),
		})
		if err := correctionStageWrittenID("create Golden group tombstone", previousID, written, err); err != nil {
			return err
		}
		successors[previousID] = supersession.RevisionID
		affected[previousID] = struct{}{}
	}
	for _, state := range authority.states {
		if _, found := affected[state.GroupRevisionID]; !found {
			continue
		}
		written, err := querier.CreateGoldenCorrectionStateTombstone(ctx, sqlc.CreateGoldenCorrectionStateTombstoneParams{
			CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID,
			RosterID: mutation.Authority.RosterID, GroupRevisionID: state.GroupRevisionID,
			StateRevisionID: state.StateRevisionID, PayloadDigest: state.PayloadDigest,
			TombstonedAt: tstz(changedAt), CreatedAt: tstz(changedAt),
		})
		if err := correctionStageWrittenID("create Golden state tombstone", state.StateRevisionID, written, err); err != nil {
			return err
		}
	}
	for _, position := range authority.positions {
		if _, found := affected[position.GroupRevisionID]; !found {
			continue
		}
		written, err := querier.CreateGoldenCorrectionPositionTombstone(ctx, sqlc.CreateGoldenCorrectionPositionTombstoneParams{
			CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID,
			RosterID: mutation.Authority.RosterID, GroupRevisionID: position.GroupRevisionID,
			LedgerRevisionID: position.LedgerRevisionID, PayloadDigest: position.PayloadDigest,
			TombstonedAt: tstz(changedAt), CreatedAt: tstz(changedAt),
		})
		if err := correctionStageWrittenID("create Golden position tombstone", position.LedgerRevisionID, written, err); err != nil {
			return err
		}
	}
	cancelled := make(map[uuid.UUID]struct{}, len(mutation.Stage.CancelledAttempts))
	for _, attempt := range mutation.Stage.CancelledAttempts {
		row, found := authority.attempts[attempt.ID]
		successor, superseded := successors[row.GroupRevisionID]
		if !found || !superseded || attempt.GroupRevisionID != successor || !row.RetainedAt.Valid ||
			(row.SnapshotState != string(domain.GoldenAttemptStatePlanned) &&
				row.SnapshotState != string(domain.GoldenAttemptStateWaitingReady)) {
			return domain.ErrConflict
		}
		reason := "superseded by result correction"
		physical, err := querier.CancelCorrectionGoldenAttempt(ctx, sqlc.CancelCorrectionGoldenAttemptParams{
			CancelledAt: tstz(changedAt), CancellationReason: &reason, AttemptID: attempt.ID,
			TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
			ExpectedState: row.AttemptState,
		})
		if err := correctionStageWrittenID("cancel retained Golden attempt", attempt.ID, physical.ID, err); err != nil {
			return err
		}
		written, err := querier.CreateGoldenCorrectionAttemptTombstone(ctx, sqlc.CreateGoldenCorrectionAttemptTombstoneParams{
			CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID,
			RosterID: mutation.Authority.RosterID, GroupRevisionID: row.GroupRevisionID,
			SuccessorRevisionID: successor.UUID(), StateRevisionID: row.StateRevisionID,
			AttemptID: attempt.ID, PriorState: row.SnapshotState, RetainedAt: row.RetainedAt,
			CancelledAt: tstz(changedAt), CancellationReason: reason,
			StatePayloadDigest: row.StatePayloadDigest, CreatedAt: tstz(changedAt),
		})
		if err := correctionStageWrittenID("create Golden attempt tombstone", attempt.ID, written, err); err != nil {
			return err
		}
		cancelled[attempt.ID] = struct{}{}
	}
	for attemptID, row := range authority.attempts {
		if _, found := affected[row.GroupRevisionID]; !found || !row.RetainedAt.Valid ||
			(row.SnapshotState != string(domain.GoldenAttemptStatePlanned) &&
				row.SnapshotState != string(domain.GoldenAttemptStateWaitingReady)) {
			continue
		}
		if _, found := cancelled[attemptID]; !found {
			return domain.ErrConflict
		}
	}
	return nil
}

func correctionStagePersistenceTransition(result correctionusecase.StageResult) (string, domain.TournamentState, error) {
	switch result.Transition {
	case correctionusecase.StagePlayoffToGolden:
		return "correction_start_golden", domain.TournamentStateGolden, nil
	case correctionusecase.StageGoldenToPlayoff:
		return "correction_start_playoffs", domain.TournamentStatePlayoffs, nil
	case correctionusecase.StageGoldenGroupsChanged:
		return "correction_refresh_golden", domain.TournamentStateGolden, nil
	default:
		return "", "", domain.ErrValidation
	}
}

type correctionPlayoffStageMaterialization struct {
	topFour ProjectionArtifactInput
	bracket ProjectionArtifactInput
	matches [2]projection.CanonicalBracketMatch
}

func persistTournamentAdminCorrectionPlayoffStage(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	input CorrectionInput,
	changedAt time.Time,
) error {
	stage, err := correctionPlayoffStageMaterializationFrom(input.ProjectionArtifacts)
	if err != nil {
		return err
	}
	ids, err := tournamentprogression.PlayoffPublicationIdentity(mutation.Command.CommandID)
	if err != nil {
		return err
	}
	scoreIDs := [2]domain.SeriesScoreRevisionID{
		ids.FirstSemifinalScoreRevisionID,
		ids.SecondSemifinalScoreRevisionID,
	}
	for index, match := range stage.matches {
		written, writeErr := querier.CreateTournamentProgressionLockedSemifinalSeries(
			ctx,
			sqlc.CreateTournamentProgressionLockedSemifinalSeriesParams{
				SeriesID: match.SeriesID, TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID,
				FirstParticipantID: match.FirstParticipantID, SecondParticipantID: match.SecondParticipantID,
				InitialScoreRevisionID: nullableUUIDValue(scoreIDs[index].UUID()), CreatedAt: tstz(changedAt),
				CommandID: mutation.Command.CommandID, SourceProjectionRevisionID: mutation.Authority.ProjectionRevisionID,
				SourceProjectionRevision: mutation.Authority.ProjectionRevision,
			},
		)
		if err := correctionStageWrittenID("create playoff semifinal", match.SeriesID, written, writeErr); err != nil {
			return err
		}
	}
	written, err := querier.CreateTournamentStagePlayoffEvidence(ctx, sqlc.CreateTournamentStagePlayoffEvidenceParams{
		CommandID: mutation.Command.CommandID, TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID,
		SourceProjectionRevisionID:    mutation.Authority.ProjectionRevisionID,
		SourceProjectionRevision:      mutation.Authority.ProjectionRevision,
		PublishedProjectionRevisionID: input.ProjectionIDs.RevisionID,
		PublishedProjectionRevision:   mutation.Authority.ProjectionRevision + 1,
		Top4ArtifactID:                stage.topFour.ID, BracketArtifactID: stage.bracket.ID,
		Top4NodeID: ids.Top4NodeID, BracketNodeID: ids.BracketNodeID,
		FirstSemifinalSeriesID: stage.matches[0].SeriesID, SecondSemifinalSeriesID: stage.matches[1].SeriesID,
		ProofDigest: mutation.Stage.ProofDigest[:], CreatedAt: tstz(changedAt),
	})
	if err := correctionStageWrittenID("create playoff evidence", mutation.Command.CommandID, written, err); err != nil {
		return err
	}
	if err := persistTournamentAdminCorrectionPlayoffGraph(ctx, querier, mutation, stage, ids, changedAt); err != nil {
		return err
	}
	for _, match := range stage.matches {
		position, conversionErr := correctionInt16(match.Position)
		if conversionErr != nil {
			return conversionErr
		}
		written, writeErr := querier.CreateTournamentStagePlayoffSemifinal(ctx, sqlc.CreateTournamentStagePlayoffSemifinalParams{
			CommandID: mutation.Command.CommandID, TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID,
			BracketArtifactID: stage.bracket.ID, Position: position, SeriesID: match.SeriesID, CreatedAt: tstz(changedAt),
		})
		if err := correctionStageWrittenID("bind playoff semifinal", match.SeriesID, written, writeErr); err != nil {
			return err
		}
	}
	settlements, err := querier.LockTournamentProgressionGoldenPositionCommits(
		ctx,
		sqlc.LockTournamentProgressionGoldenPositionCommitsParams{
			TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID,
			SourceProjectionRevisionID: mutation.Authority.ProjectionRevisionID,
			SourceProjectionRevision:   mutation.Authority.ProjectionRevision,
		},
	)
	if err != nil {
		return tournamentAdminCorrectionError("lock corrected playoff Golden settlements", err)
	}
	for _, settlement := range settlements {
		written, writeErr := querier.CreateTournamentStagePlayoffGoldenSettlement(
			ctx,
			sqlc.CreateTournamentStagePlayoffGoldenSettlementParams{
				CommandID: mutation.Command.CommandID, TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID,
				GroupRevisionID: settlement.GroupRevisionID, AttemptID: settlement.AttemptID,
				PositionCommitID: settlement.PositionCommitID, ParticipantID: settlement.ParticipantID,
				Position: settlement.Position, CreatedAt: tstz(changedAt),
			},
		)
		if err := correctionStageWrittenID("bind corrected playoff Golden settlement", settlement.PositionCommitID, written, writeErr); err != nil {
			return err
		}
	}
	return nil
}

func correctionPlayoffStageMaterializationFrom(
	artifacts []ProjectionArtifactInput,
) (correctionPlayoffStageMaterialization, error) {
	var result correctionPlayoffStageMaterialization
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case domain.ArtifactKindTopFour:
			result.topFour = artifact
		case domain.ArtifactKindBracket:
			result.bracket = artifact
		}
	}
	if result.topFour.ID == uuid.Nil || result.bracket.ID == uuid.Nil ||
		len(result.topFour.Members) != 4 || len(result.bracket.Members) != 4 {
		return correctionPlayoffStageMaterialization{}, domain.ErrConflict
	}
	var document struct {
		Rounds []projection.CanonicalBracketMatch `json:"rounds"`
	}
	if json.Unmarshal(result.bracket.Payload, &document) != nil || len(document.Rounds) != len(result.matches) {
		return correctionPlayoffStageMaterialization{}, domain.ErrConflict
	}
	for index, match := range document.Rounds {
		if match.Position != index+1 || match.SeriesID == uuid.Nil || match.State != domain.SeriesStateLocked ||
			match.FirstWins != 0 || match.SecondWins != 0 ||
			match.FirstParticipantID != result.topFour.Members[index*2].ParticipantID ||
			match.SecondParticipantID != result.topFour.Members[index*2+1].ParticipantID {
			return correctionPlayoffStageMaterialization{}, domain.ErrConflict
		}
		result.matches[index] = match
	}
	return result, nil
}

func persistTournamentAdminCorrectionPlayoffGraph(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	stage correctionPlayoffStageMaterialization,
	ids tournamentprogression.PlayoffPublicationIDs,
	changedAt time.Time,
) error {
	written, err := querier.CreateTournamentProgressionStageProjectionNodeAuthority(
		ctx,
		sqlc.CreateTournamentProgressionStageProjectionNodeAuthorityParams{
			ID: ids.StageNodeAuthorityID, TournamentID: mutation.Command.TournamentID,
			RosterID: mutation.Authority.RosterID, StageCommandID: nullableUUIDValue(mutation.Command.CommandID),
			CreatedAt: tstz(changedAt),
		},
	)
	if err := correctionStageWrittenID("create playoff node authority", ids.StageNodeAuthorityID, written, err); err != nil {
		return err
	}
	nodes := []struct {
		id      uuid.UUID
		kind    domain.ArtifactKind
		entity  uuid.UUID
		payload []byte
		digest  [sha256.Size]byte
	}{
		{ids.Top4NodeID, domain.ArtifactKindTopFour, mutation.Command.TournamentID, stage.topFour.Payload, stage.topFour.PayloadDigest},
		{ids.BracketNodeID, domain.ArtifactKindBracket, mutation.Command.TournamentID, stage.bracket.Payload, stage.bracket.PayloadDigest},
	}
	scoreIDs := [2]domain.SeriesScoreRevisionID{ids.FirstSemifinalScoreRevisionID, ids.SecondSemifinalScoreRevisionID}
	scoreNodes := [2]uuid.UUID{ids.FirstSemifinalScoreNodeID, ids.SecondSemifinalScoreNodeID}
	for index, match := range stage.matches {
		payload, marshalErr := json.Marshal(map[string]any{
			"schema": "result-projection-series-score-genesis-v1", "stage_command_id": mutation.Command.CommandID.String(),
			"series_id": match.SeriesID.String(), "revision_id": scoreIDs[index].UUID().String(),
			"first_wins": 0, "second_wins": 0,
		})
		if marshalErr != nil {
			return marshalErr
		}
		nodes = append(nodes, struct {
			id      uuid.UUID
			kind    domain.ArtifactKind
			entity  uuid.UUID
			payload []byte
			digest  [sha256.Size]byte
		}{scoreNodes[index], domain.ArtifactKindSeriesScore, match.SeriesID, payload, sha256.Sum256(payload)})
	}
	for _, node := range nodes {
		revision, previous, lineageErr := correctionPlayoffStageNodeLineage(
			ctx, querier, mutation, node.kind, node.entity,
		)
		if lineageErr != nil {
			return lineageErr
		}
		if err := querier.CreateResultProjectionNode(ctx, sqlc.CreateResultProjectionNodeParams{
			ID: node.id, AuthorityID: ids.StageNodeAuthorityID, TournamentID: mutation.Command.TournamentID,
			RosterID: mutation.Authority.RosterID, ArtifactKind: string(node.kind), EntityID: node.entity,
			RevisionNumber: revision, PreviousNodeID: previous,
			Payload: node.payload, PayloadDigest: node.digest[:], CreatedAt: tstz(changedAt),
		}); err != nil {
			return correctionStageWriteError(fmt.Sprintf("create playoff node %s/%s", node.kind, node.entity), err)
		}
		stored, readErr := querier.GetResultProjectionNode(ctx, node.id)
		if readErr != nil || stored.ID != node.id || stored.AuthorityID != ids.StageNodeAuthorityID ||
			stored.TournamentID != mutation.Command.TournamentID || stored.RosterID != mutation.Authority.RosterID ||
			stored.ArtifactKind != string(node.kind) || stored.EntityID != node.entity ||
			stored.RevisionNumber != revision || stored.PreviousNodeID != previous ||
			!bytes.Equal(stored.Payload, node.payload) || !bytes.Equal(stored.PayloadDigest, node.digest[:]) {
			if readErr != nil {
				return tournamentAdminCorrectionError("read corrected playoff node", readErr)
			}
			return domain.ErrConflict
		}
	}
	for _, edge := range [][2]uuid.UUID{
		{ids.Top4NodeID, ids.BracketNodeID},
		{ids.BracketNodeID, ids.FirstSemifinalScoreNodeID},
		{ids.BracketNodeID, ids.SecondSemifinalScoreNodeID},
	} {
		written, writeErr := querier.CreateTournamentProgressionStageProjectionDependency(
			ctx,
			sqlc.CreateTournamentProgressionStageProjectionDependencyParams{
				AuthorityID: ids.StageNodeAuthorityID, SourceNodeID: edge[0], DerivedNodeID: edge[1], CreatedAt: tstz(changedAt),
			},
		)
		if err := correctionStageWrittenID("create playoff node dependency", ids.StageNodeAuthorityID, written, writeErr); err != nil {
			return err
		}
	}
	return nil
}

func correctionPlayoffStageNodeLineage(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	kind domain.ArtifactKind,
	entityID uuid.UUID,
) (int64, uuid.NullUUID, error) {
	latest, err := querier.LockCorrectionLatestProjectionNode(ctx, sqlc.LockCorrectionLatestProjectionNodeParams{
		TournamentID: mutation.Command.TournamentID, RosterID: mutation.Authority.RosterID,
		ArtifactKind: string(kind), EntityID: entityID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, uuid.NullUUID{}, nil
	}
	if err != nil {
		return 0, uuid.NullUUID{}, tournamentAdminCorrectionError("lock corrected playoff node lineage", err)
	}
	if latest.ID == uuid.Nil || latest.TournamentID != mutation.Command.TournamentID ||
		latest.RosterID != mutation.Authority.RosterID || latest.ArtifactKind != string(kind) ||
		latest.EntityID != entityID || latest.RevisionNumber < 1 || latest.RevisionNumber == math.MaxInt64 {
		return 0, uuid.NullUUID{}, domain.ErrConflict
	}
	return latest.RevisionNumber + 1, nullableUUIDValue(latest.ID), nil
}

func persistTournamentAdminCorrectionGoldenGroups(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	input CorrectionInput,
	snapshot correctionusecase.StageSnapshot,
	changedAt time.Time,
) error {
	resultingProjectionRevision := mutation.Authority.ProjectionRevision + 1
	for _, group := range mutation.Stage.Corrected.GoldenGroups {
		positionFrom, err := correctionInt16(group.PositionFrom)
		if err != nil {
			return err
		}
		positionTo, err := correctionInt16(group.PositionTo)
		if err != nil {
			return err
		}
		groupProof, groupDigest, err := correctionGoldenGroupDocument(group)
		if err != nil {
			return err
		}
		id, err := querier.CreateCorrectionStageTieGroup(ctx, sqlc.CreateCorrectionStageTieGroupParams{
			CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
			GroupID: group.ID, GroupRevisionID: group.RevisionID.UUID(),
			SourceProjectionRevisionID: input.ProjectionIDs.RevisionID,
			SourceProjectionRevision:   resultingProjectionRevision,
			PositionFrom:               positionFrom, PositionTo: positionTo, Proof: groupProof, ProofDigest: groupDigest[:], CreatedAt: tstz(changedAt),
		})
		if err := correctionStageWrittenID("create Golden tie group", group.ID, id, err); err != nil {
			return err
		}
		for index, member := range group.Members {
			standingPosition, err := correctionInt16(group.PositionFrom + index)
			if err != nil {
				return err
			}
			id, err = querier.CreateCorrectionStageTieGroupMember(ctx, sqlc.CreateCorrectionStageTieGroupMemberParams{
				CommandID: mutation.Command.CommandID, TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
				GroupID: group.ID, ParticipantID: member.ParticipantID, StandingPosition: standingPosition, CreatedAt: tstz(changedAt),
			})
			if err := correctionStageWrittenID("create Golden tie member", member.ParticipantID, id, err); err != nil {
				return err
			}
		}
		id, err = querier.CreateCorrectionGoldenGroupRevision(ctx, sqlc.CreateCorrectionGoldenGroupRevisionParams{
			RevisionID: group.RevisionID.UUID(), GroupID: group.ID, StageProgressionCommandID: mutation.Command.CommandID,
			TournamentID: snapshot.TournamentID, RosterID: mutation.Authority.RosterID,
			SourceProjectionRevisionID: input.ProjectionIDs.RevisionID,
			SourceProjectionRevision:   resultingProjectionRevision,
			PositionFrom:               positionFrom, PositionTo: positionTo, Definition: groupProof, DefinitionDigest: groupDigest[:], CreatedAt: tstz(changedAt),
		})
		if err := correctionStageWrittenID("create Golden group revision", group.RevisionID.UUID(), id, err); err != nil {
			return err
		}
	}
	return nil
}

func correctionGoldenGroupDocument(group domain.GoldenGroupState) ([]byte, [sha256.Size]byte, error) {
	type member struct {
		ParticipantID uuid.UUID `json:"participant_id"`
	}
	document := struct {
		Schema       string    `json:"schema"`
		GroupID      uuid.UUID `json:"group_id"`
		RevisionID   uuid.UUID `json:"revision_id"`
		PositionFrom int       `json:"position_from"`
		PositionTo   int       `json:"position_to"`
		Members      []member  `json:"members"`
	}{
		Schema: "tournament-correction-golden-group-v1", GroupID: group.ID, RevisionID: group.RevisionID.UUID(),
		PositionFrom: group.PositionFrom, PositionTo: group.PositionTo, Members: make([]member, len(group.Members)),
	}
	for index, value := range group.Members {
		document.Members[index] = member{ParticipantID: value.ParticipantID}
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("marshal correction Golden group: %w", err)
	}
	return payload, sha256.Sum256(payload), nil
}

func correctionStageWrittenID(operation string, want, got uuid.UUID, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("correction stage %s: %w", operation, domain.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf(
			"correction stage %s: %w",
			operation,
			mapRepositoryWriteError("TournamentAdminCorrectionPostgres - "+operation, err),
		)
	}
	if want == uuid.Nil || got != want {
		return fmt.Errorf("correction stage %s returned %s, want %s: %w", operation, got, want, domain.ErrConflict)
	}
	return nil
}

func correctionStageWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("correction stage %s: %w", operation, domain.ErrConflict)
	}
	return mapRepositoryWriteError("TournamentAdminCorrectionPostgres - "+operation, err)
}

func correctionInt16(value int) (int16, error) {
	if value < 1 || value > domain.TournamentMaxParticipants {
		return 0, domain.ErrValidation
	}
	return int16(value), nil //nolint:gosec // bounded by TournamentMaxParticipants.
}
