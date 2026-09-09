package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// TournamentAdminReplayPostgres persists the two admin replay commands. Each
// method uses the transaction supplied by ReplayWorkflow; it never opens an
// independent transaction around a partial replay transition.
type TournamentAdminReplayPostgres struct {
	tx *TxManager
}

func NewTournamentAdminReplayPostgres(tx *TxManager) *TournamentAdminReplayPostgres {
	return &TournamentAdminReplayPostgres{tx: tx}
}

func (r *TournamentAdminReplayPostgres) ReadReplayTime(
	ctx context.Context,
	commandID uuid.UUID,
) (time.Time, error) {
	if ctx == nil || !r.available() || commandID == uuid.Nil {
		return time.Time{}, domain.ErrValidation
	}
	value, err := r.tx.Querier(ctx).GetTournamentAdminReplayTime(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("TournamentAdminReplayPostgres - read time: %w", err)
	}
	value.Time = value.Time.Round(0).UTC()
	if !value.Valid || !domain.IsValidServerTime(value.Time) {
		return time.Time{}, domain.ErrInternal
	}
	return value.Time, nil
}

func (r *TournamentAdminReplayPostgres) LoadOperatorReserveAuthority(
	ctx context.Context,
	command tournamentadmin.ReserveCommand,
) (tournamentadmin.OperatorReserveAuthority, error) {
	if ctx == nil || !r.available() {
		return tournamentadmin.OperatorReserveAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	source, err := r.loadSource(ctx, querier, replaySourceScope{
		TournamentID: command.TournamentID, OldWaveID: command.OldWaveID,
		SeriesID: command.SeriesID, SlotID: command.SlotID,
		FailedGameID: command.AssignmentAttemptID, AssignmentID: command.AssignmentID,
	})
	if err != nil {
		return tournamentadmin.OperatorReserveAuthority{}, err
	}

	currentRow, err := querier.FindOperatorReplayReserveCommand(ctx, command.CommandID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return tournamentadmin.OperatorReserveAuthority{}, fmt.Errorf("TournamentAdminReplayPostgres - find reserve command: %w", err)
	}
	if err == nil {
		current, err := r.rehydrateOperatorReserve(ctx, querier, source, command, currentRow)
		if err != nil {
			return tournamentadmin.OperatorReserveAuthority{}, err
		}
		return tournamentadmin.OperatorReserveAuthority{
			TournamentState: domain.TournamentState(source.row.Tournament.State),
			Replay: gameusecase.OperatorReserveAuthority{
				Scope: replayScopeFromCommand(command), Revision: source.row.Series.Revision,
				Exhaustion: current.Exhaustion, Reserve: reserveAuthorityFromRecord(current.Reserve),
				Current: current,
			},
		}, nil
	}

	exhaustionRow, err := querier.LockReplayReserveExhaustionForOperatorReserve(ctx,
		sqlc.LockReplayReserveExhaustionForOperatorReserveParams{
			ExhaustionCommandID: command.ExpectedExhaustionCommandID, TournamentID: command.TournamentID,
			OldWaveID: command.OldWaveID, SeriesID: command.SeriesID, SlotID: command.SlotID,
			AssignmentID: command.AssignmentID,
		})
	if err != nil {
		return tournamentadmin.OperatorReserveAuthority{}, replayWorkflowLookupError("lock reserve exhaustion", err)
	}
	exhaustion, err := source.replayExhaustion(exhaustionRow)
	if err != nil {
		return tournamentadmin.OperatorReserveAuthority{}, err
	}
	authority, err := r.loadReserveAuthority(ctx, querier, source, command)
	if err != nil {
		return tournamentadmin.OperatorReserveAuthority{}, err
	}
	return tournamentadmin.OperatorReserveAuthority{
		TournamentState: domain.TournamentState(source.row.Tournament.State),
		Replay: gameusecase.OperatorReserveAuthority{
			Scope: replayScopeFromCommand(command), Revision: source.row.Series.Revision,
			Exhaustion: exhaustion, Reserve: authority,
		},
	}, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentAdminReplayPostgres) CommitOperatorReserve(
	ctx context.Context,
	command tournamentadmin.ReserveCommand,
	requestDigest [sha256.Size]byte,
	record gameusecase.OperatorReserve,
) (*gameusecase.OperatorReserve, bool, error) {
	if ctx == nil || !r.available() || record.Validate() != nil {
		return nil, false, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	if existing, err := querier.FindOperatorReplayReserveCommand(ctx, command.CommandID); err == nil {
		return r.reconcileStoredOperatorReserve(ctx, querier, command, requestDigest, existing)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("TournamentAdminReplayPostgres - find reserve before commit: %w", err)
	}

	source, err := r.loadSource(ctx, querier, replaySourceScope{
		TournamentID: command.TournamentID, OldWaveID: command.OldWaveID,
		SeriesID: command.SeriesID, SlotID: command.SlotID,
		FailedGameID: command.AssignmentAttemptID, AssignmentID: command.AssignmentID,
	})
	if err != nil {
		return nil, false, err
	}
	if source.row.Series.Revision != record.ExpectedAuthorityRevision ||
		domain.SeriesState(source.row.Series.State) != domain.SeriesStateTechnicalPause {
		return nil, false, domain.ErrConflict
	}
	reserveAuthority, err := r.loadReserveAuthority(ctx, querier, source, command)
	if err != nil {
		return nil, false, err
	}
	expectedReserve, err := assignmentusecase.BuildReserveAssignmentRecord(
		operatorReserveAssignmentCommand(command, record.Reserve.PromotedAt), reserveAuthority,
	)
	if err != nil || expectedReserve.ProofDigest != record.Reserve.ProofDigest {
		return nil, false, domain.ErrConflict
	}
	authorityRevision, err := r.reserveAuthorityRevision(ctx, querier, source, command)
	if err != nil {
		return nil, false, err
	}

	edgeID := replayDerivedID(command.CommandID, "operator-reserve-edge")
	reservationID := replayDerivedID(command.CommandID, "operator-reserve-reservation")
	document := operatorReserveDocumentFromRecord(command, record)
	encodedDocument, err := encodeReplayStorageDocument(document)
	if err != nil {
		return nil, false, err
	}
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	selectionEvidence, err := json.Marshal(record.Reserve.Evidence)
	if err != nil {
		return nil, false, fmt.Errorf("TournamentAdminReplayPostgres - encode reserve evidence: %w", err)
	}
	hints, err := json.Marshal(record.Reserve.Snapshot.Hints)
	if err != nil {
		return nil, false, fmt.Errorf("TournamentAdminReplayPostgres - encode reserve hints: %w", err)
	}

	createdAt := tstz(record.Reserve.PromotedAt)
	if _, err = querier.CreateOperatorReplayReserveCommand(ctx, sqlc.CreateOperatorReplayReserveCommandParams{
		CommandID: command.CommandID, ExhaustionCommandID: command.ExpectedExhaustionCommandID,
		TournamentID: command.TournamentID, RosterID: source.row.Roster.ID, OldWaveID: command.OldWaveID,
		SeriesID: command.SeriesID, SlotID: command.SlotID, AssignmentID: command.AssignmentID,
		AssignmentAttemptID: command.AssignmentAttemptID, FailedGameID: command.AssignmentAttemptID,
		ClosureRevisionID: record.Exhaustion.ClosureRevisionID.UUID(), FromSnapshotID: record.Reserve.FromSnapshotID,
		ActorID: command.Operator.ActorID, Reason: command.Reason,
		ExpectedAssignmentRevision: command.ExpectedAssignmentRevision,
		ExpectedPoolRevisionID:     command.ExpectedPoolRevisionID, ExpectedPoolRevision: command.ExpectedPoolRevision,
		ExpectedHistoryRevisionID: command.ExpectedHistoryRevisionID, ExpectedHistoryRevision: command.ExpectedHistoryRevision,
		ExpectedArtifactRevisionID: command.ExpectedArtifactRevisionID, ExpectedArtifactRevision: command.ExpectedArtifactRevision,
		ExpectedReservationRevisionID: command.ExpectedReservationRevisionID, ExpectedReservationRevision: command.ExpectedReservationRevision,
		ExpectedCategoryRevisionID: command.ExpectedCategoryRevisionID, ExpectedCategoryRevision: command.ExpectedCategoryRevision,
		ProposedTaskID: record.Reserve.Snapshot.TaskID, ProposedVersion: int32(record.Reserve.Snapshot.Version), //nolint:gosec // Domain validation bounds this value before the storage conversion.
		ProposedSnapshotID: record.Reserve.Snapshot.SnapshotID, EvidenceID: record.Reserve.Evidence.ID,
		EdgeID: edgeID, ReservationID: reservationID, SourceSeriesRevision: record.ExpectedAuthorityRevision,
		ResultingSeriesRevision: record.ExpectedAuthorityRevision + 1,
		RequestDigest:           requestDigest[:], EvidenceDigest: record.Reserve.Evidence.Digest[:],
		ProofDigest: record.Reserve.ProofDigest[:], ContentDigest: record.Reserve.ContentDigest[:],
		AuthorityDocument: encodedDocument, RecordDocument: encodedDocument,
		PromotedAt: createdAt, CreatedAt: createdAt,
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create operator reserve command", err)
	}
	if _, err = querier.CreateOperatorReplayReserveEdge(ctx, sqlc.CreateOperatorReplayReserveEdgeParams{
		ID: edgeID, PlanID: source.row.AssignmentPlan.ID, BranchID: source.row.AssignmentBranch.ID,
		TaskID: record.Reserve.Snapshot.TaskID, TaskVersion: int32(record.Reserve.Snapshot.Version), //nolint:gosec // Domain validation bounds this value before the storage conversion.
		OperatorReserveCommandID: nullableUUIDValue(command.CommandID), SelectionEvidence: selectionEvidence,
		CreatedAt: createdAt,
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create operator reserve edge", err)
	}
	if _, err = querier.CreateOperatorReplayReserveReservation(ctx, sqlc.CreateOperatorReplayReserveReservationParams{
		ID: reservationID, EdgeID: edgeID, PlanID: source.row.AssignmentPlan.ID,
		BranchID: source.row.AssignmentBranch.ID, TaskID: record.Reserve.Snapshot.TaskID,
		TaskVersion: int32(record.Reserve.Snapshot.Version), CommittedAt: createdAt, CreatedAt: createdAt, //nolint:gosec // Domain validation bounds this value before the storage conversion.
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create operator reserve reservation", err)
	}
	if _, err = querier.CreateOperatorReplayReserveSnapshot(ctx, sqlc.CreateOperatorReplayReserveSnapshotParams{
		ID: record.Reserve.Snapshot.SnapshotID, ReservationID: reservationID,
		TaskID: record.Reserve.Snapshot.TaskID, TaskVersion: int32(record.Reserve.Snapshot.Version), //nolint:gosec // Domain validation bounds this value before the storage conversion.
		Title: record.Reserve.Snapshot.Title, Description: record.Reserve.Snapshot.Description,
		Category: string(record.Reserve.Snapshot.Category), Difficulty: string(record.Reserve.Snapshot.Difficulty),
		TimeLimit: int32(record.Reserve.Snapshot.TimeLimit), Flag: record.Reserve.Snapshot.Flag, //nolint:gosec // Domain validation bounds this value before the storage conversion.
		Hints: hints, TaskUrl: record.Reserve.Snapshot.TaskURL, SourceFileUrl: record.Reserve.Snapshot.SourceFileURL,
		ContentDigest: record.Reserve.ContentDigest[:], CreatedAt: createdAt,
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create operator reserve snapshot", err)
	}
	if _, err = querier.AdvanceReplayReserveAuthorityCAS(ctx, sqlc.AdvanceReplayReserveAuthorityCASParams{
		UpdatedAt: createdAt, AssignmentID: command.AssignmentID, ExpectedRevision: authorityRevision,
		ExpectedSnapshotID: record.Reserve.FromSnapshotID,
	}); err != nil {
		return nil, false, replayWorkflowCASWriteError("advance reserve authority", err)
	}
	if _, err = querier.TransitionReplaySeriesCAS(ctx, sqlc.TransitionReplaySeriesCASParams{
		NextState: string(domain.SeriesStateReplayRequired), UpdatedAt: createdAt, SeriesID: command.SeriesID,
		RosterID: source.row.Roster.ID, ExpectedRevision: record.ExpectedAuthorityRevision,
		ExpectedState: string(domain.SeriesStateTechnicalPause),
	}); err != nil {
		return nil, false, replayWorkflowCASWriteError("resume replay Series", err)
	}
	result := record
	return &result, true, nil
}

func (r *TournamentAdminReplayPostgres) LoadReplayReplacementAuthority(
	ctx context.Context,
	command tournamentadmin.ReplayCommand,
) (tournamentadmin.ReplayReplacementAuthority, error) {
	if ctx == nil || !r.available() {
		return tournamentadmin.ReplayReplacementAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	source, err := r.loadSource(ctx, querier, replaySourceScope{
		TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
		SlotID: command.SlotID, FailedGameID: command.FailedGameID, AssignmentID: command.AssignmentID,
	})
	if err != nil {
		return tournamentadmin.ReplayReplacementAuthority{}, err
	}
	currentRow, err := querier.FindReplayReplacementCommand(ctx, command.CommandID)
	hasCurrent, lookupErr := replayReplacementLookupState(err)
	if lookupErr != nil {
		return tournamentadmin.ReplayReplacementAuthority{}, fmt.Errorf("TournamentAdminReplayPostgres - find replacement command: %w", lookupErr)
	}
	reserveRow, err := querier.LockOperatorReplayReserveForReplacement(ctx, sqlc.LockOperatorReplayReserveForReplacementParams{
		TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
		SlotID: command.SlotID, AssignmentID: command.AssignmentID, AssignmentAttemptID: command.FailedGameID,
		FailedGameID: command.FailedGameID,
	})
	if err != nil {
		return tournamentadmin.ReplayReplacementAuthority{}, replayWorkflowLookupError("lock operator reserve", err)
	}
	chain, _, err := r.loadReplayReserveChain(ctx, querier, source, &reserveRow.CommandID)
	if err != nil {
		return tournamentadmin.ReplayReplacementAuthority{}, err
	}
	exhaustionRow, err := querier.LockReplayReserveExhaustionForOperatorReserve(ctx,
		sqlc.LockReplayReserveExhaustionForOperatorReserveParams{ExhaustionCommandID: reserveRow.ExhaustionCommandID,
			TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
			SlotID: command.SlotID, AssignmentID: command.AssignmentID})
	if err != nil {
		return tournamentadmin.ReplayReplacementAuthority{}, replayWorkflowLookupError("reload replay exhaustion", err)
	}
	failed, closure, err := source.failedAttemptAndClosure(reserveRow.ExhaustionCommandID, &exhaustionRow)
	if err != nil {
		return tournamentadmin.ReplayReplacementAuthority{}, err
	}
	authority := gameusecase.ReplayReplacementAuthority{
		Scope: replayScopeFromReplayCommand(command), Revision: source.row.Series.Revision,
		FailedAttempt: failed, OldWaveClosure: closure, ReserveChain: chain,
		ParticipantIDs: [2]uuid.UUID{source.row.Series.FirstParticipantID, source.row.Series.SecondParticipantID},
	}
	if hasCurrent {
		current, _, currentErr := replayReplacementFromStored(source, command, currentRow, chain)
		if currentErr != nil {
			return tournamentadmin.ReplayReplacementAuthority{}, currentErr
		}
		authority.Current = current
	}
	if authority.Current == nil && domain.SeriesState(source.row.Series.State) != domain.SeriesStateReplayRequired {
		return tournamentadmin.ReplayReplacementAuthority{}, domain.ErrConflict
	}
	return tournamentadmin.ReplayReplacementAuthority{
		TournamentState: domain.TournamentState(source.row.Tournament.State), Replay: authority,
	}, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentAdminReplayPostgres) CommitReplayReplacement(
	ctx context.Context,
	command tournamentadmin.ReplayCommand,
	requestDigest [sha256.Size]byte,
	replacement gameusecase.ReplayReplacement,
) (*gameusecase.ReplayReplacement, bool, error) {
	if ctx == nil || !r.available() || replacement.Validate() != nil {
		return nil, false, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	if existing, err := querier.FindReplayReplacementCommand(ctx, command.CommandID); err == nil {
		if !bytesEqual(existing.RequestDigest, requestDigest[:]) {
			return nil, false, gameusecase.ErrReplayReplacementReuse
		}
		source, sourceErr := r.loadSource(ctx, querier, replaySourceScope{
			TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
			SlotID: command.SlotID, FailedGameID: command.FailedGameID, AssignmentID: command.AssignmentID,
		})
		if sourceErr != nil {
			return nil, false, sourceErr
		}
		reserveRow, reserveErr := querier.LockOperatorReplayReserveForReplacement(ctx, sqlc.LockOperatorReplayReserveForReplacementParams{
			TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
			SlotID: command.SlotID, AssignmentID: command.AssignmentID, AssignmentAttemptID: command.FailedGameID,
			FailedGameID: command.FailedGameID,
		})
		if reserveErr != nil {
			return nil, false, replayWorkflowLookupError("lock operator reserve for replay retry", reserveErr)
		}
		chain, _, chainErr := r.loadReplayReserveChain(ctx, querier, source, &reserveRow.CommandID)
		if chainErr != nil {
			return nil, false, chainErr
		}
		return replayReplacementFromStored(source, command, existing, chain)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("TournamentAdminReplayPostgres - find replacement before commit: %w", err)
	}

	source, err := r.loadSource(ctx, querier, replaySourceScope{
		TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
		SlotID: command.SlotID, FailedGameID: command.FailedGameID, AssignmentID: command.AssignmentID,
	})
	if err != nil {
		return nil, false, err
	}
	if source.row.Series.Revision != replacement.ExpectedAuthorityRevision ||
		domain.SeriesState(source.row.Series.State) != domain.SeriesStateReplayRequired {
		return nil, false, domain.ErrConflict
	}
	reserveRow, err := querier.LockOperatorReplayReserveForReplacement(ctx, sqlc.LockOperatorReplayReserveForReplacementParams{
		TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
		SlotID: command.SlotID, AssignmentID: command.AssignmentID, AssignmentAttemptID: command.FailedGameID,
		FailedGameID: command.FailedGameID,
	})
	if err != nil {
		return nil, false, replayWorkflowLookupError("lock operator reserve for replay", err)
	}
	chain, reservations, err := r.loadReplayReserveChain(ctx, querier, source, &reserveRow.CommandID)
	if err != nil {
		return nil, false, err
	}
	index := replacement.ReservePosition - 1
	if index < 0 || index >= len(chain.Snapshots) || chain.Snapshots[index].SnapshotID != replacement.Snapshot.SnapshotID {
		return nil, false, domain.ErrConflict
	}
	reservation := reservations[index]
	document := replayReplacementDocumentFromReplacement(command, replacement)
	encodedDocument, err := encodeReplayStorageDocument(document)
	if err != nil {
		return nil, false, err
	}
	openedAt := tstz(replacement.OpenedAt)
	if _, err = querier.CreateReplayReplacementCommand(ctx, sqlc.CreateReplayReplacementCommandParams{
		CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: source.row.Roster.ID,
		OldWaveID: command.OldWaveID, SeriesID: command.SeriesID, SlotID: command.SlotID,
		AssignmentID: command.AssignmentID, AssignmentAttemptID: command.FailedGameID,
		FailedGameID: command.FailedGameID, ClosureRevisionID: replacement.ClosureRevisionID.UUID(),
		FromSnapshotID: replacement.FromSnapshotID, ReplacementAssignmentAttemptID: replacement.AssignmentAttemptID,
		ReplacementGameID: replacement.Game.ID, ReplacementWaveID: replacement.Wave.ID,
		ReplacementWaveRevisionID: replacement.Wave.RevisionID.UUID(),
		ReadyWindowID:             replacement.Wave.ReadyWindow.ID,
		ReadyWindowRevisionID:     replacement.Wave.ReadyWindow.RevisionID.UUID(),
		SnapshotID:                replacement.Snapshot.SnapshotID, ReservePosition: int16(replacement.ReservePosition), //nolint:gosec // Domain validation bounds this value before the storage conversion.
		SourceSeriesRevision:    replacement.ExpectedAuthorityRevision,
		ResultingSeriesRevision: replacement.ExpectedAuthorityRevision + 1,
		ActorID:                 command.Operator.ActorID, Reason: command.Reason, RequestDigest: requestDigest[:],
		AuthorityDocument: encodedDocument, RecordDocument: encodedDocument, OpenedAt: openedAt, CreatedAt: openedAt,
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create replay replacement command", err)
	}
	if _, err = querier.CreateReplayReplacementWave(ctx, sqlc.CreateReplayReplacementWaveParams{
		ID: replacement.Wave.ID, TournamentID: command.TournamentID, RosterID: source.row.Roster.ID,
		RevisionID: replacement.Wave.RevisionID.UUID(), ReplacesWaveID: nullableUUIDValue(command.OldWaveID),
		CreatedAt: openedAt,
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create replacement Wave", err)
	}
	for _, member := range replacement.Wave.Members {
		if err = querier.CreateReplayReplacementWaveMember(ctx, sqlc.CreateReplayReplacementWaveMemberParams{
			WaveID: replacement.Wave.ID, RosterID: source.row.Roster.ID, ParticipantID: member.ParticipantID, CreatedAt: openedAt,
		}); err != nil {
			return nil, false, replayWorkflowWriteError("create replacement Wave member", err)
		}
		if _, err = querier.CreateReplayReplacementReadinessHead(ctx, sqlc.CreateReplayReplacementReadinessHeadParams{
			WaveID: replacement.Wave.ID, RosterID: source.row.Roster.ID, ParticipantID: member.ParticipantID, CreatedAt: openedAt,
		}); err != nil {
			return nil, false, replayWorkflowWriteError("create replacement readiness", err)
		}
	}
	if err = querier.CreateReplayReplacementWaveSeries(ctx, sqlc.CreateReplayReplacementWaveSeriesParams{
		WaveID: replacement.Wave.ID, TournamentID: command.TournamentID, RosterID: source.row.Roster.ID,
		SeriesID: command.SeriesID, CreatedAt: openedAt,
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create replacement Wave Series", err)
	}
	if _, err = querier.CreateReplayReplacementGameAttempt(ctx, sqlc.CreateReplayReplacementGameAttemptParams{
		ID: replacement.Game.ID, SlotID: replacement.Slot.ID, SeriesID: command.SeriesID,
		RosterID: source.row.Roster.ID, AttemptNumber: int32(replacement.Game.AttemptNo), CreatedAt: openedAt, //nolint:gosec // Domain validation bounds this value before the storage conversion.
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create replacement Game", err)
	}
	if _, err = querier.CreateReplayReplacementAssignment(ctx, sqlc.CreateReplayReplacementAssignmentParams{
		ID: replacement.AssignmentAttemptID, AttemptID: replacement.Game.ID, SeriesID: command.SeriesID,
		RosterID: source.row.Roster.ID, PlanID: source.row.AssignmentPlan.ID, BranchID: source.row.AssignmentBranch.ID,
		ReservationID: reservation.ID, SnapshotID: replacement.Snapshot.SnapshotID,
		TaskID: replacement.Snapshot.TaskID, TaskVersion: int32(replacement.Snapshot.Version), CreatedAt: openedAt, //nolint:gosec // Domain validation bounds this value before the storage conversion.
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create replacement assignment", err)
	}
	if source.row.AssignmentPlan.Kind == "exact_draft" {
		if err = createReplayReserveAuthorityTx(ctx, querier, replacement.AssignmentAttemptID); err != nil {
			return nil, false, replayWorkflowWriteError("create replacement replay authority", err)
		}
	}
	if _, err = querier.CreateReplayReplacementReadyWindow(ctx, sqlc.CreateReplayReplacementReadyWindowParams{
		ID: replacement.Wave.ReadyWindow.ID, WaveID: replacement.Wave.ID, RosterID: source.row.Roster.ID,
		RevisionID: replacement.Wave.ReadyWindow.RevisionID.UUID(), OpenedAt: openedAt,
		Deadline: tstz(replacement.Wave.ReadyWindow.Deadline),
	}); err != nil {
		return nil, false, replayWorkflowWriteError("create replacement ready window", err)
	}
	bound, err := querier.BindReplayReplacementReadinessHeads(ctx, sqlc.BindReplayReplacementReadinessHeadsParams{
		ReadyWindowID: nullableUUIDValue(replacement.Wave.ReadyWindow.ID), UpdatedAt: openedAt, WaveID: replacement.Wave.ID,
	})
	if err != nil || !sameReplayParticipants(bound, replacement.Wave.Members) {
		if err != nil {
			return nil, false, replayWorkflowWriteError("bind replacement readiness", err)
		}
		return nil, false, domain.ErrConflict
	}
	if _, err = querier.OpenReplayReplacementWaveCAS(ctx, sqlc.OpenReplayReplacementWaveCASParams{
		OpenedAt: openedAt, WaveID: replacement.Wave.ID, TournamentID: command.TournamentID,
	}); err != nil {
		return nil, false, replayWorkflowCASWriteError("open replacement Wave", err)
	}
	if _, err = querier.TransitionReplaySeriesCAS(ctx, sqlc.TransitionReplaySeriesCASParams{
		NextState: string(domain.SeriesStateReady), UpdatedAt: openedAt, SeriesID: command.SeriesID,
		RosterID: source.row.Roster.ID, ExpectedRevision: replacement.ExpectedAuthorityRevision,
		ExpectedState: string(domain.SeriesStateReplayRequired),
	}); err != nil {
		return nil, false, replayWorkflowCASWriteError("ready replay Series", err)
	}
	result := replacement
	return &result, true, nil
}

func (r *TournamentAdminReplayPostgres) available() bool {
	return r != nil && r.tx != nil
}

func replayDerivedID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("tournament-admin-replay:"+role))
}

func replayScopeFromCommand(command tournamentadmin.ReserveCommand) gameusecase.ReplayReplacementScope {
	return gameusecase.ReplayReplacementScope{TournamentID: command.TournamentID, OldWaveID: command.OldWaveID,
		SeriesID: command.SeriesID, SlotID: command.SlotID, AssignmentID: command.AssignmentID}
}

func replayScopeFromReplayCommand(command tournamentadmin.ReplayCommand) gameusecase.ReplayReplacementScope {
	return gameusecase.ReplayReplacementScope{TournamentID: command.TournamentID, OldWaveID: command.OldWaveID,
		SeriesID: command.SeriesID, SlotID: command.SlotID, AssignmentID: command.AssignmentID}
}

func replayWorkflowLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return fmt.Errorf("TournamentAdminReplayPostgres - %s: %w", operation, err)
}

func replayWorkflowWriteError(operation string, err error) error {
	return mapRepositoryWriteError("TournamentAdminReplayPostgres - "+operation, err)
}

func replayWorkflowCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return replayWorkflowWriteError(operation, err)
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func replayReplacementLookupState(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}

type replaySourceScope struct {
	TournamentID uuid.UUID
	OldWaveID    uuid.UUID
	SeriesID     uuid.UUID
	SlotID       uuid.UUID
	FailedGameID uuid.UUID
	AssignmentID uuid.UUID
}

type replaySource struct {
	row          sqlc.LockReplayWorkflowSourceRow
	graph        []sqlc.LockReplayWorkflowSeriesGraphRow
	gameHeads    []sqlc.LockReplayWorkflowGameResultHeadsRow
	scoreHead    sqlc.LockReplayWorkflowScoreHeadRow
	waveRows     []sqlc.LockReplayWorkflowOldWaveExecutionRow
	routes       []sqlc.WaveMemberRoute
	receipts     []sqlc.TaskDeliveryReceipt
	participants []sqlc.LockReplayWorkflowParticipantReservationsRow
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentAdminReplayPostgres) loadSource(
	ctx context.Context,
	querier *sqlc.Queries,
	scope replaySourceScope,
) (replaySource, error) {
	row, err := querier.LockReplayWorkflowSource(ctx, sqlc.LockReplayWorkflowSourceParams{
		TournamentID: scope.TournamentID, OldWaveID: scope.OldWaveID, SeriesID: scope.SeriesID,
		SlotID: scope.SlotID, FailedGameID: scope.FailedGameID, AssignmentID: scope.AssignmentID,
	})
	if err != nil {
		return replaySource{}, replayWorkflowLookupError("lock replay source", err)
	}
	if row.Tournament.ID != scope.TournamentID || row.Roster.TournamentID != scope.TournamentID ||
		row.Series.ID != scope.SeriesID || row.GameSlot.ID != scope.SlotID ||
		row.GameAttempt.ID != scope.FailedGameID || row.Assignment.ID != scope.AssignmentID ||
		row.Assignment.AttemptID != scope.FailedGameID || row.Assignment.SnapshotID != row.TaskSnapshot.ID ||
		row.Series.RosterID != row.Roster.ID || row.GameAttempt.RosterID != row.Roster.ID ||
		row.Assignment.RosterID != row.Roster.ID || row.AssignmentPlan.RosterID != row.Roster.ID ||
		row.AssignmentBranch.PlanID != row.AssignmentPlan.ID {
		return replaySource{}, domain.ErrConflict
	}
	graph, err := querier.LockReplayWorkflowSeriesGraph(ctx, sqlc.LockReplayWorkflowSeriesGraphParams{
		SeriesID: row.Series.ID, RosterID: row.Roster.ID,
	})
	if err != nil {
		return replaySource{}, replayWorkflowLookupError("lock replay Series graph", err)
	}
	headRows, err := querier.LockReplayWorkflowGameResultHeads(ctx, sqlc.LockReplayWorkflowGameResultHeadsParams{
		SeriesID: row.Series.ID, RosterID: row.Roster.ID,
	})
	if err != nil {
		return replaySource{}, replayWorkflowLookupError("lock replay Game heads", err)
	}
	scoreHead, err := querier.LockReplayWorkflowScoreHead(ctx, sqlc.LockReplayWorkflowScoreHeadParams{
		SeriesID: row.Series.ID, RosterID: row.Roster.ID,
	})
	if err != nil {
		return replaySource{}, replayWorkflowLookupError("lock replay score head", err)
	}
	waveRows, err := querier.LockReplayWorkflowOldWaveExecution(ctx, sqlc.LockReplayWorkflowOldWaveExecutionParams{
		WaveID: row.Wave.ID, RosterID: row.Roster.ID,
	})
	if err != nil {
		return replaySource{}, replayWorkflowLookupError("lock replay old Wave", err)
	}
	routes, err := querier.LockReplayWorkflowRoutes(ctx, sqlc.LockReplayWorkflowRoutesParams{
		TournamentID: row.Tournament.ID, RosterID: row.Roster.ID, WaveID: row.Wave.ID,
	})
	if err != nil {
		return replaySource{}, replayWorkflowLookupError("lock replay routes", err)
	}
	receipts, err := querier.LockReplayWorkflowReceipts(ctx, row.Assignment.ID)
	if err != nil {
		return replaySource{}, replayWorkflowLookupError("lock replay delivery history", err)
	}
	participantIDs := []uuid.UUID{row.Series.FirstParticipantID, row.Series.SecondParticipantID}
	participants, err := querier.LockReplayWorkflowParticipantReservations(ctx,
		sqlc.LockReplayWorkflowParticipantReservationsParams{RosterID: row.Roster.ID, ParticipantIds: participantIDs})
	if err != nil {
		return replaySource{}, replayWorkflowLookupError("lock replay participants", err)
	}
	source := replaySource{
		row: row, graph: graph, gameHeads: headRows, scoreHead: scoreHead, waveRows: waveRows,
		routes: routes, receipts: receipts, participants: participants,
	}
	if _, err := source.seriesExecution(domain.SeriesState(row.Series.State), nil); err != nil {
		return replaySource{}, err
	}
	return source, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (source replaySource) seriesExecution(
	state domain.SeriesState,
	resumeState *domain.SeriesState,
) (seriesdomain.Execution, error) {
	if !state.IsValid() || source.row.Series.ID == uuid.Nil || source.row.Series.TournamentID == uuid.Nil ||
		source.row.Series.RosterID != source.row.Roster.ID || source.row.Series.FirstParticipantID == source.row.Series.SecondParticipantID ||
		source.scoreHead.CurrentRevisionID == uuid.Nil || source.scoreHead.HeadRevision < 1 {
		return seriesdomain.Execution{}, errReplayWorkflowAuthority
	}
	headByGame := make(map[uuid.UUID]sqlc.LockReplayWorkflowGameResultHeadsRow, len(source.gameHeads))
	for _, head := range source.gameHeads {
		if head.GameAttemptID == uuid.Nil || head.ResultRevisionID == uuid.Nil || head.ResultHeadRevision < 1 ||
			head.RevisionNumber < 1 || !head.CreatedAt.Valid || !head.OccurredAt.Valid ||
			head.ResultState == "" || head.ResultReason == "" {
			return seriesdomain.Execution{}, errReplayWorkflowAuthority
		}
		if _, duplicate := headByGame[head.GameAttemptID]; duplicate {
			return seriesdomain.Execution{}, errReplayWorkflowAuthority
		}
		headByGame[head.GameAttemptID] = head
	}
	slots := make([]domain.GameSlot, 0, len(source.graph))
	for index := 0; index < len(source.graph); {
		first := source.graph[index]
		slotRow := first.GameSlot
		if slotRow.SeriesID != source.row.Series.ID || slotRow.RosterID != source.row.Roster.ID ||
			slotRow.ID == uuid.Nil || slotRow.SlotNumber < 1 {
			return seriesdomain.Execution{}, errReplayWorkflowAuthority
		}
		slot := domain.GameSlot{
			ID: slotRow.ID, SeriesID: slotRow.SeriesID, Position: int(slotRow.SlotNumber),
			Category: domain.Category(slotRow.Category), ScoreBefore: domain.SeriesScore{
				FirstParticipantWins:  int(slotRow.FirstParticipantWinsBefore),
				SecondParticipantWins: int(slotRow.SecondParticipantWinsBefore),
			},
		}
		for index < len(source.graph) && source.graph[index].GameSlot.ID == slot.ID {
			gameRow := source.graph[index].GameAttempt
			if gameRow.SeriesID != source.row.Series.ID || gameRow.RosterID != source.row.Roster.ID || gameRow.SlotID != slot.ID {
				return seriesdomain.Execution{}, errReplayWorkflowAuthority
			}
			game, err := replayGameFromRow(gameRow, headByGame)
			if err != nil {
				return seriesdomain.Execution{}, err
			}
			slot.Attempts = append(slot.Attempts, game)
			index++
		}
		if err := slot.Validate(); err != nil {
			return seriesdomain.Execution{}, fmt.Errorf("%w: slot: %w", errReplayWorkflowAuthority, err)
		}
		slots = append(slots, slot)
	}
	series := domain.Series{
		ID: source.row.Series.ID, TournamentID: source.row.Series.TournamentID,
		FirstParticipantID: source.row.Series.FirstParticipantID, SecondParticipantID: source.row.Series.SecondParticipantID,
		Format: domain.SeriesFormat(source.row.Series.Format), State: state,
		Score: domain.SeriesScore{FirstParticipantWins: int(source.row.Series.FirstParticipantWins), SecondParticipantWins: int(source.row.Series.SecondParticipantWins)},
		Slots: slots,
	}
	if source.row.Series.WinnerID.Valid {
		winner := source.row.Series.WinnerID.UUID
		series.WinnerID = &winner
	}
	if source.row.Series.CurrentScoreRevisionID.Valid {
		value := domain.SeriesScoreRevisionID(source.row.Series.CurrentScoreRevisionID.UUID)
		series.CurrentScoreRevisionID = &value
	}
	if source.row.Series.CurrentResultRevisionID.Valid {
		value := domain.OfficialResultRevisionID(source.row.Series.CurrentResultRevisionID.UUID)
		series.CurrentResultRevisionID = &value
	}
	if state != domain.SeriesStateCompleted && state != domain.SeriesStateCancelled {
		series.WinnerID = nil
		series.CurrentResultRevisionID = nil
	}
	execution := seriesdomain.Execution{Series: series, ResumeState: resumeState}
	if err := execution.Validate(); err != nil {
		return seriesdomain.Execution{}, fmt.Errorf("%w: Series: %w", errReplayWorkflowAuthority, err)
	}
	return execution, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func replayGameFromRow(
	row sqlc.GameAttempt,
	headByGame map[uuid.UUID]sqlc.LockReplayWorkflowGameResultHeadsRow,
) (domain.Game, error) {
	game := domain.Game{ID: row.ID, SlotID: row.SlotID, AttemptNo: int(row.AttemptNumber), State: domain.GameState(row.State)}
	if row.State == string(domain.GameStatePlanned) || row.State == string(domain.GameStateReady) ||
		row.State == string(domain.GameStateActive) || row.State == string(domain.GameStatePaused) {
		if row.ResultReason != nil || row.WinnerID.Valid || row.ResultRevisionID.Valid {
			return domain.Game{}, errReplayWorkflowAuthority
		}
	} else {
		if !replayResultReasonIsValid(row) || !row.ResultRevisionID.Valid {
			return domain.Game{}, errReplayWorkflowAuthority
		}
		game.ResultReason = domain.GameResultReason(*row.ResultReason)
		resultRevisionID := domain.OfficialResultRevisionID(row.ResultRevisionID.UUID)
		game.ResultRevisionID = &resultRevisionID
		if row.WinnerID.Valid {
			winner := row.WinnerID.UUID
			game.WinnerID = &winner
		}
		head, found := headByGame[row.ID]
		if !found || head.ResultRevisionID != row.ResultRevisionID.UUID ||
			head.ResultState != row.State || head.ResultReason != *row.ResultReason ||
			(head.WinnerID.Valid != row.WinnerID.Valid) || (head.WinnerID.Valid && head.WinnerID.UUID != row.WinnerID.UUID) {
			return domain.Game{}, errReplayWorkflowAuthority
		}
	}
	if err := game.Validate(); err != nil {
		return domain.Game{}, fmt.Errorf("%w: Game: %w", errReplayWorkflowAuthority, err)
	}
	return game, nil
}

func replayResultReasonIsValid(row sqlc.GameAttempt) bool {
	return row.ResultReason != nil && *row.ResultReason != ""
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (source replaySource) oldWave() (domain.Wave, error) {
	if domain.WaveState(source.row.Wave.State) != domain.WaveStateCompleted ||
		!source.row.Wave.StartedAt.Valid || source.row.Wave.RevisionID == uuid.Nil || len(source.waveRows) != 2 {
		return domain.Wave{}, errReplayWorkflowAuthority
	}
	first := source.waveRows[0]
	window := first.ReadyWindow
	if window.ID == uuid.Nil || window.WaveID != source.row.Wave.ID || window.RosterID != source.row.Roster.ID ||
		window.RevisionID == uuid.Nil || window.State != string(domain.ReadyWindowStateConsumed) ||
		!window.OpenedAt.Valid || !window.Deadline.Valid || !window.ConsumedAt.Valid {
		return domain.Wave{}, errReplayWorkflowAuthority
	}
	consumedAt := window.ConsumedAt.Time.Round(0).UTC()
	startedAt := source.row.Wave.StartedAt.Time.Round(0).UTC()
	wave := domain.Wave{
		ID: source.row.Wave.ID, TournamentID: source.row.Wave.TournamentID,
		RevisionID: domain.WaveRevisionID(source.row.Wave.RevisionID), State: domain.WaveState(source.row.Wave.State),
		StartedAt: &startedAt,
		ReadyWindow: &domain.ReadyWindow{
			ID: window.ID, WaveID: window.WaveID, RevisionID: domain.ReadyWindowRevisionID(window.RevisionID),
			State: domain.ReadyWindowState(window.State), OpenedAt: window.OpenedAt.Time.Round(0).UTC(),
			Deadline: window.Deadline.Time.Round(0).UTC(), ConsumedAt: &consumedAt,
		},
	}
	seen := make(map[uuid.UUID]struct{}, len(source.waveRows))
	for _, item := range source.waveRows {
		if item.ReadyWindow.ID != window.ID || item.WaveMember.WaveID != wave.ID ||
			item.WaveMember.RosterID != source.row.Roster.ID || item.WaveReadiness.WaveID != wave.ID ||
			item.WaveReadiness.RosterID != source.row.Roster.ID || !item.WaveReadiness.ReadyWindowID.Valid ||
			item.WaveReadiness.ReadyWindowID.UUID != window.ID || !item.WaveReadiness.Ready {
			return domain.Wave{}, errReplayWorkflowAuthority
		}
		if _, duplicate := seen[item.WaveMember.ParticipantID]; duplicate || item.WaveMember.ParticipantID == uuid.Nil {
			return domain.Wave{}, errReplayWorkflowAuthority
		}
		seen[item.WaveMember.ParticipantID] = struct{}{}
		wave.Members = append(wave.Members, domain.WaveMember{ParticipantID: item.WaveMember.ParticipantID, Ready: true})
	}
	if err := wave.Validate(); err != nil {
		return domain.Wave{}, fmt.Errorf("%w: old Wave: %w", errReplayWorkflowAuthority, err)
	}
	return wave, nil
}

func (source replaySource) failedAttemptAndClosure(
	exhaustionCommandID uuid.UUID,
	exhaustion *sqlc.ReplayReserveExhaustion,
) (gameusecase.AttemptRecord, gameusecase.Closure, error) {
	if exhaustionCommandID == uuid.Nil {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}
	var document replayReserveExhaustionDocument
	if exhaustion != nil {
		decoded, err := decodeReplayStorageDocument[replayReserveExhaustionDocument](exhaustion.RecordDocument)
		if err != nil || validateReplayReserveExhaustionDocument(decoded, *exhaustion) != nil {
			return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
		}
		document = decoded
	} else {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, domain.ErrConflict
	}
	return source.failedAttemptAndClosureDocument(document)
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (source replaySource) failedAttemptAndClosureDocument(
	document replayReserveExhaustionDocument,
) (gameusecase.AttemptRecord, gameusecase.Closure, error) {
	if document.Scope.TournamentID != source.row.Tournament.ID || document.Scope.OldWaveID != source.row.Wave.ID ||
		document.Scope.SeriesID != source.row.Series.ID || document.Scope.SlotID != source.row.GameSlot.ID ||
		document.Scope.AssignmentID != source.row.Assignment.ID || document.AssignmentAttemptID != source.row.GameAttempt.ID ||
		document.FailedGameID != source.row.GameAttempt.ID || document.ActiveSnapshotID != source.row.TaskSnapshot.ID {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}
	wave, err := source.oldWave()
	if err != nil {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, err
	}
	route, ok := source.failedRoute(document)
	if !ok {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}
	closure := gameusecase.Closure{
		Scope:     gameusecase.CloseScope{TournamentID: source.row.Tournament.ID, WaveID: wave.ID},
		CommandID: document.ClosureCommandID, ExpectedAuthorityRevision: document.ClosureAuthorityRevision,
		PreviousWaveRevisionID: domain.WaveRevisionID(document.PreviousClosureRevisionID), Wave: wave,
		Children: []gameusecase.CloseChild{{SeriesID: source.row.Series.ID, SlotID: source.row.GameSlot.ID,
			GameID: source.row.GameAttempt.ID, State: domain.GameStateVoid, RouteID: route.ID}},
		ClosedAt: source.row.Wave.ClosedAt.Time.Round(0).UTC(),
	}
	if !source.row.Wave.ClosedAt.Valid || closure.Validate() != nil {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}

	previous, err := source.seriesExecution(domain.SeriesStateReplayRequired, nil)
	if err != nil {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, err
	}
	failedGame, found := replayFindGame(previous.Series, source.row.GameAttempt.ID)
	if !found || failedGame.State != domain.GameStateVoid || failedGame.ResultRevisionID == nil {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}
	failure, err := gamedomain.ClassifyFailure(gamedomain.FailureClass(document.FailureClass), domain.Category(document.Category))
	if err != nil || failedGame.ResultReason != failure.Reason {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}
	head, found := source.gameHead(source.row.GameAttempt.ID)
	if !found || head.ResultRevisionID != failedGame.ResultRevisionID.UUID() || !head.OccurredAt.Valid ||
		!source.scoreHead.ScoreRecordedAt.Valid || source.scoreHead.ScoreRevisionID != source.scoreHead.CurrentRevisionID {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}
	resultIDs, err := source.gameResultRevisionIDs()
	if err != nil {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, err
	}
	gameRevisionID := domain.OfficialResultRevisionID(head.ResultRevisionID)
	scoreRevisionID := domain.SeriesScoreRevisionID(source.scoreHead.ScoreRevisionID)
	terminalizedAt := head.OccurredAt.Time.Round(0).UTC()
	if !source.scoreHead.ScoreRecordedAt.Time.Round(0).UTC().Equal(terminalizedAt) ||
		!route.RoutedAt.Time.Round(0).UTC().Equal(terminalizedAt) {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}
	record := gameusecase.AttemptRecord{
		Scope: domain.FailedAttemptScope{TournamentID: source.row.Tournament.ID, WaveID: source.row.Wave.ID,
			SeriesID: source.row.Series.ID, SlotID: source.row.GameSlot.ID, GameID: source.row.GameAttempt.ID,
			AssignmentID: source.row.Assignment.ID, AssignmentAttemptID: source.row.GameAttempt.ID},
		CommandID: document.FailedAttemptCommandID, ExpectedAuthorityRevision: document.FailedAttemptAuthorityRevision,
		ActiveSnapshotID: source.row.TaskSnapshot.ID, Failure: failure, Series: previous, Game: failedGame,
		AttemptGameResultRevision: gameusecase.AttemptGameResultRevision{Ordinal: document.GameResultOrdinal, ID: gameRevisionID,
			GameID: failedGame.ID, Reason: failure.Reason, RecordedAt: terminalizedAt},
		ScoreRevision: seriesdomain.ScoreRevision{ID: scoreRevisionID, SeriesID: source.row.Series.ID,
			FirstParticipantID: source.row.Series.FirstParticipantID, SecondParticipantID: source.row.Series.SecondParticipantID,
			PreviousRevisionID: optionalScoreRevisionID(source.scoreHead.PreviousRevisionID), Ordinal: document.ScoreOrdinal,
			Format: domain.SeriesFormat(source.row.Series.Format), ScoreBefore: previous.Series.Score,
			ScoreAfter: previous.Series.Score, GameResultRevisionIDs: resultIDs, RecordedAt: terminalizedAt},
		WaveRoute: gameusecase.WaveMemberRoute{ID: route.ID, WaveID: route.WaveID, SeriesID: route.SeriesID,
			SlotID: route.SlotID, GameID: route.GameAttemptID, Category: domain.Category(route.Category), RoutedAt: route.RoutedAt.Time},
		Evidence: seriesdomain.SettlementEvidence{AuditEventID: document.AuditEventID, OutboxEventID: document.OutboxEventID,
			ProjectionRevisionID: document.ProjectionRevisionID, SourceProjectionRevision: document.SourceProjectionRevision,
			ProjectionRevision: document.SourceProjectionRevision + 1, RecordedAt: terminalizedAt},
		TerminalizedAt: terminalizedAt,
	}
	if record.Validate() != nil {
		return gameusecase.AttemptRecord{}, gameusecase.Closure{}, errReplayWorkflowAuthority
	}
	return record, closure, nil
}

func (source replaySource) replayExhaustion(row sqlc.ReplayReserveExhaustion) (gameusecase.ReplayReserveExhaustion, error) {
	document, err := decodeReplayStorageDocument[replayReserveExhaustionDocument](row.RecordDocument)
	if err != nil || validateReplayReserveExhaustionDocument(document, row) != nil {
		return gameusecase.ReplayReserveExhaustion{}, errReplayWorkflowAuthority
	}
	failed, closure, err := source.failedAttemptAndClosureDocument(document)
	if err != nil {
		return gameusecase.ReplayReserveExhaustion{}, err
	}
	previous := failed.Series
	pausedState := domain.SeriesStateTechnicalPause
	paused, err := source.seriesExecution(pausedState, &previous.Series.State)
	if err != nil || source.row.Series.Revision < row.ResultingSeriesRevision {
		return gameusecase.ReplayReserveExhaustion{}, errReplayWorkflowAuthority
	}
	exhaustion := gameusecase.ReplayReserveExhaustion{
		Scope: gameusecase.ReplayReplacementScope{TournamentID: row.TournamentID, OldWaveID: row.OldWaveID,
			SeriesID: row.SeriesID, SlotID: row.SlotID, AssignmentID: row.AssignmentID},
		CommandID: row.CommandID, ExpectedAuthorityRevision: document.ExpectedAuthorityRevision,
		ClosureRevisionID: domain.WaveRevisionID(row.ClosureRevisionID), FailedAttemptCommandID: document.FailedAttemptCommandID,
		AssignmentAttemptID: row.AssignmentAttemptID, GameID: row.FailedGameID, ActiveSnapshotID: row.ActiveSnapshotID,
		ReservePosition: int(row.ReservePosition), Category: domain.Category(row.Category), PreviousSeries: previous,
		Series: paused, OldWave: closure.Wave,
	}
	if exhaustion.Validate() != nil {
		return gameusecase.ReplayReserveExhaustion{}, errReplayWorkflowAuthority
	}
	return exhaustion, nil
}

func (source replaySource) failedRoute(document replayReserveExhaustionDocument) (sqlc.WaveMemberRoute, bool) {
	for _, route := range source.routes {
		if route.ID == document.RouteID && route.SeriesID == source.row.Series.ID &&
			route.SlotID == source.row.GameSlot.ID && route.GameAttemptID == source.row.GameAttempt.ID &&
			route.Category == document.Category && route.RoutedAt.Valid {
			result := route
			result.RoutedAt.Time = result.RoutedAt.Time.Round(0).UTC()
			return result, true
		}
	}
	return sqlc.WaveMemberRoute{}, false
}

func (source replaySource) gameHead(gameID uuid.UUID) (sqlc.LockReplayWorkflowGameResultHeadsRow, bool) {
	for _, head := range source.gameHeads {
		if head.GameAttemptID == gameID {
			return head, true
		}
	}
	return sqlc.LockReplayWorkflowGameResultHeadsRow{}, false
}

func (source replaySource) gameResultRevisionIDs() ([]domain.OfficialResultRevisionID, error) {
	result := make([]domain.OfficialResultRevisionID, 0, len(source.gameHeads))
	for _, head := range source.gameHeads {
		if head.ResultRevisionID == uuid.Nil {
			return nil, errReplayWorkflowAuthority
		}
		result = append(result, domain.OfficialResultRevisionID(head.ResultRevisionID))
	}
	if err := seriesdomain.ValidateGameResultRevisionIDs(result); err != nil {
		return nil, errReplayWorkflowAuthority
	}
	return result, nil
}

func replayFindGame(series domain.Series, gameID uuid.UUID) (domain.Game, bool) {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return domain.Game{}, false
}

func optionalScoreRevisionID(value uuid.NullUUID) *domain.SeriesScoreRevisionID {
	if !value.Valid {
		return nil
	}
	result := domain.SeriesScoreRevisionID(value.UUID)
	return &result
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (r *TournamentAdminReplayPostgres) loadReserveAuthority(
	ctx context.Context,
	querier *sqlc.Queries,
	source replaySource,
	command tournamentadmin.ReserveCommand,
) (assignmentusecase.ReserveAssignmentAuthority, error) {
	row, err := querier.LockReplayReserveAuthority(ctx, sqlc.LockReplayReserveAuthorityParams{
		TournamentID: command.TournamentID, RosterID: source.row.Roster.ID, SeriesID: command.SeriesID,
		SlotID: command.SlotID, AssignmentID: command.AssignmentID, AssignmentAttemptID: command.AssignmentAttemptID,
		ExpectedSnapshotID: command.ExpectedSnapshotID, ExpectedAssignmentRevision: command.ExpectedAssignmentRevision,
		ExpectedPoolRevisionID: command.ExpectedPoolRevisionID, ExpectedPoolRevision: command.ExpectedPoolRevision,
		ExpectedHistoryRevisionID: command.ExpectedHistoryRevisionID, ExpectedHistoryRevision: command.ExpectedHistoryRevision,
		ExpectedArtifactRevisionID: command.ExpectedArtifactRevisionID, ExpectedArtifactRevision: command.ExpectedArtifactRevision,
		ExpectedReservationRevisionID: command.ExpectedReservationRevisionID, ExpectedReservationRevision: command.ExpectedReservationRevision,
		ExpectedCategoryRevisionID: command.ExpectedCategoryRevisionID, ExpectedCategoryRevision: command.ExpectedCategoryRevision,
		ProposedTaskID: command.ProposedTaskID, ProposedVersion: int32(command.ProposedVersion), //nolint:gosec // Domain validation bounds this value before the storage conversion.
	})
	if err != nil {
		return assignmentusecase.ReserveAssignmentAuthority{}, replayWorkflowLookupError("lock reserve authority", err)
	}
	if row.Revision < 1 || row.AssignmentID != command.AssignmentID || row.TournamentID != command.TournamentID ||
		row.RosterID != source.row.Roster.ID || row.SeriesID != command.SeriesID || row.SlotID != command.SlotID ||
		row.AssignmentAttemptID != command.AssignmentAttemptID || row.CurrentAssignmentID != command.AssignmentID ||
		row.CurrentAssignmentRevision != command.ExpectedAssignmentRevision || row.CurrentSnapshotID != command.ExpectedSnapshotID ||
		row.CandidateTaskRowID != row.CandidateTaskID || row.CandidateVersion != int32(command.ProposedVersion) || //nolint:gosec // Domain validation bounds this value before the storage conversion.
		len(row.CandidateContentDigest) != sha256.Size {
		return assignmentusecase.ReserveAssignmentAuthority{}, errReplayWorkflowAuthority
	}
	poolRows, err := querier.LockReplayReserveAuthorityPool(ctx, command.AssignmentID)
	if err != nil {
		return assignmentusecase.ReserveAssignmentAuthority{}, replayWorkflowLookupError("lock reserve pool", err)
	}
	pool := domain.TaskPoolRevision{ID: row.PoolRevisionID, Revision: row.PoolRevision, Kind: domain.AssignmentTaskKindNormal}
	for _, item := range poolRows {
		if item.AssignmentID != command.AssignmentID || item.TaskID == uuid.Nil || item.TaskVersion < 1 {
			return assignmentusecase.ReserveAssignmentAuthority{}, errReplayWorkflowAuthority
		}
		pool.Versions = append(pool.Versions, domain.TaskVersionRef{TaskID: item.TaskID, Version: int(item.TaskVersion)})
	}
	candidate := domain.AssignmentTaskSnapshot{
		SnapshotID: command.ProposedSnapshotID, TaskID: row.CandidateTaskID, Version: int(row.CandidateVersion),
		Kind: domain.AssignmentTaskKindNormal, Title: row.CandidateTitle, Description: row.CandidateDescription,
		Category: domain.Category(row.CandidateCategory), Difficulty: domain.Difficulty(row.CandidateDifficulty),
		TimeLimit: int(row.CandidateTimeLimit), Flag: row.CandidateFlag,
		Hints:   taskHintsToDomain(row.CandidateHint1, row.CandidateHint2, row.CandidateHint3),
		TaskURL: row.CandidateTaskUrl, SourceFileURL: row.CandidateSourceFileUrl,
	}
	if candidate.Validate() != nil {
		return assignmentusecase.ReserveAssignmentAuthority{}, errReplayWorkflowAuthority
	}
	contentDigest := [sha256.Size]byte{}
	copy(contentDigest[:], row.CandidateContentDigest)
	reservations, err := source.participantReservationAuthority()
	if err != nil {
		return assignmentusecase.ReserveAssignmentAuthority{}, err
	}
	receipts, err := source.receiptHistory()
	if err != nil {
		return assignmentusecase.ReserveAssignmentAuthority{}, err
	}
	authority := assignmentusecase.ReserveAssignmentAuthority{
		Scope: assignmentusecase.ReserveAssignmentScope{TournamentID: command.TournamentID, AssignmentID: command.AssignmentID,
			AttemptID: command.AssignmentAttemptID, SlotID: command.SlotID},
		Revisions: assignmentusecase.ReserveAssignmentSourceRevisions{AssignmentRevision: row.AssignmentRevision,
			PoolRevisionID: row.PoolRevisionID, PoolRevision: row.PoolRevision, HistoryRevisionID: row.HistoryRevisionID,
			HistoryRevision: row.HistoryRevision, ArtifactRevisionID: row.ArtifactRevisionID, ArtifactRevision: row.ArtifactRevision,
			ReservationRevisionID: row.ReservationRevisionID, ReservationRevision: row.ReservationRevision,
			CategoryRevisionID: row.CategoryRevisionID, CategoryRevision: row.CategoryRevision},
		CurrentSnapshotID: row.ActiveSnapshotID, RequiredCategory: domain.Category(row.RequiredCategory),
		ParticipantIDs:          []uuid.UUID{source.row.Series.FirstParticipantID, source.row.Series.SecondParticipantID},
		ParticipantReservations: reservations, Pool: pool, ReceiptHistory: receipts,
		CandidateHealth: domain.TaskVersionHealth{TaskID: row.CandidateTaskID, Version: int(row.CandidateVersion),
			PoolRevisionID: row.PoolRevisionID, PoolKind: domain.AssignmentTaskKindNormal, Exists: true,
			Enabled: row.TaskEnabled, Healthy: row.TaskHealthy, MutationLocked: row.TaskMutationLocked,
			PubliclyExposed: row.TaskPubliclyExposed},
		CandidateSnapshot: candidate, CandidateContentDigest: contentDigest,
	}
	if _, _, err := assignmentusecase.NormalizeReserveAssignmentAuthority(authority.Scope, authority); err != nil {
		return assignmentusecase.ReserveAssignmentAuthority{}, fmt.Errorf("%w: reserve authority: %w", errReplayWorkflowAuthority, err)
	}
	return authority, nil
}

func (source replaySource) participantReservationAuthority() ([]assignmentusecase.ExactNormalParticipantReservation, error) {
	if len(source.participants) != 2 {
		return nil, errReplayWorkflowAuthority
	}
	result := make([]assignmentusecase.ExactNormalParticipantReservation, 0, len(source.participants))
	seen := make(map[uuid.UUID]struct{}, len(source.participants))
	for _, row := range source.participants {
		if row.ParticipantID == uuid.Nil || row.PlayerID == uuid.Nil || row.ReservationID == uuid.Nil ||
			row.TournamentID != source.row.Tournament.ID || row.Revision < 1 || !row.AcquiredAt.Valid || !row.UpdatedAt.Valid ||
			row.UpdatedAt.Time.Before(row.AcquiredAt.Time) {
			return nil, errReplayWorkflowAuthority
		}
		if _, duplicate := seen[row.ParticipantID]; duplicate {
			return nil, errReplayWorkflowAuthority
		}
		seen[row.ParticipantID] = struct{}{}
		result = append(result, assignmentusecase.ExactNormalParticipantReservation{ParticipantID: row.ParticipantID, PlayerID: row.PlayerID,
			Reservation: domain.ParticipantReservation{PlayerID: row.PlayerID, ReservationID: row.ReservationID,
				TournamentID: row.TournamentID, Revision: row.Revision, AcquiredAt: row.AcquiredAt.Time.Round(0).UTC(),
				UpdatedAt: row.UpdatedAt.Time.Round(0).UTC()}})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].ParticipantID.String() < result[right].ParticipantID.String()
	})
	return result, nil
}

func (source replaySource) receiptHistory() ([]assignmentusecase.TaskReceiptRef, error) {
	result := make([]assignmentusecase.TaskReceiptRef, 0, len(source.receipts))
	participants := map[uuid.UUID]struct{}{
		source.row.Series.FirstParticipantID: {}, source.row.Series.SecondParticipantID: {},
	}
	for _, row := range source.receipts {
		if row.AssignmentID != source.row.Assignment.ID || row.AttemptID != source.row.GameAttempt.ID ||
			row.RosterID != source.row.Roster.ID || row.TaskID == uuid.Nil || row.TaskVersion < 1 ||
			row.SnapshotID == uuid.Nil || row.ParticipantID == uuid.Nil || !row.DeliveredAt.Valid {
			return nil, errReplayWorkflowAuthority
		}
		if _, found := participants[row.ParticipantID]; !found {
			return nil, errReplayWorkflowAuthority
		}
		result = append(result, assignmentusecase.TaskReceiptRef{ParticipantID: row.ParticipantID, TaskID: row.TaskID, Version: int(row.TaskVersion)})
	}
	return result, nil
}

func reserveAuthorityFromRecord(record assignmentusecase.ReserveAssignmentRecord) assignmentusecase.ReserveAssignmentAuthority {
	return assignmentusecase.ReserveAssignmentAuthority{Scope: record.Scope, Revisions: record.Revisions,
		CurrentSnapshotID: record.FromSnapshotID, RequiredCategory: record.RequiredCategory,
		ParticipantIDs:          append([]uuid.UUID(nil), record.ParticipantIDs...),
		ParticipantReservations: append([]assignmentusecase.ExactNormalParticipantReservation(nil), record.ParticipantReservations...),
		Pool:                    domain.CloneTaskPool(record.Pool), ReceiptHistory: append([]assignmentusecase.TaskReceiptRef(nil), record.ReceiptHistory...),
		CandidateHealth: record.CandidateHealth, CandidateSnapshot: record.Snapshot,
		CandidateContentDigest: record.ContentDigest, CategoryExhaustion: record.CategoryExhaustion}
}

func operatorReserveAssignmentCommand(
	command tournamentadmin.ReserveCommand,
	promotedAt time.Time,
) assignmentusecase.ReserveAssignmentCommand {
	return assignmentusecase.ReserveAssignmentCommand{
		Scope: assignmentusecase.ReserveAssignmentScope{TournamentID: command.TournamentID, AssignmentID: command.AssignmentID,
			AttemptID: command.AssignmentAttemptID, SlotID: command.SlotID},
		ExpectedSnapshotID: command.ExpectedSnapshotID, EvidenceID: command.EvidenceID,
		Mode: assignmentusecase.ReserveAssignmentModeOperator, OperatorID: command.Operator.ActorID,
		Reason: command.Reason, PromotedAt: promotedAt,
	}
}

func (r *TournamentAdminReplayPostgres) reserveAuthorityRevision(
	ctx context.Context,
	querier *sqlc.Queries,
	source replaySource,
	command tournamentadmin.ReserveCommand,
) (int64, error) {
	row, err := querier.LockReplayReserveAuthority(ctx, sqlc.LockReplayReserveAuthorityParams{
		TournamentID: command.TournamentID, RosterID: source.row.Roster.ID, SeriesID: command.SeriesID,
		SlotID: command.SlotID, AssignmentID: command.AssignmentID, AssignmentAttemptID: command.AssignmentAttemptID,
		ExpectedSnapshotID: command.ExpectedSnapshotID, ExpectedAssignmentRevision: command.ExpectedAssignmentRevision,
		ExpectedPoolRevisionID: command.ExpectedPoolRevisionID, ExpectedPoolRevision: command.ExpectedPoolRevision,
		ExpectedHistoryRevisionID: command.ExpectedHistoryRevisionID, ExpectedHistoryRevision: command.ExpectedHistoryRevision,
		ExpectedArtifactRevisionID: command.ExpectedArtifactRevisionID, ExpectedArtifactRevision: command.ExpectedArtifactRevision,
		ExpectedReservationRevisionID: command.ExpectedReservationRevisionID, ExpectedReservationRevision: command.ExpectedReservationRevision,
		ExpectedCategoryRevisionID: command.ExpectedCategoryRevisionID, ExpectedCategoryRevision: command.ExpectedCategoryRevision,
		ProposedTaskID: command.ProposedTaskID, ProposedVersion: int32(command.ProposedVersion), //nolint:gosec // Domain validation bounds this value before the storage conversion.
	})
	if err != nil {
		return 0, replayWorkflowLookupError("reload reserve authority", err)
	}
	if row.Revision < 1 {
		return 0, errReplayWorkflowAuthority
	}
	return row.Revision, nil
}

func (r *TournamentAdminReplayPostgres) rehydrateOperatorReserve(
	ctx context.Context,
	querier *sqlc.Queries,
	source replaySource,
	command tournamentadmin.ReserveCommand,
	row sqlc.OperatorReplayReserve,
) (*gameusecase.OperatorReserve, error) {
	document, err := decodeReplayStorageDocument[operatorReserveDocument](row.RecordDocument)
	if err != nil || validateOperatorReserveDocument(document, row) != nil ||
		!bytesEqual(row.RequestDigest, replayReserveCommandDigest(command)) {
		return nil, errReplayWorkflowAuthority
	}
	exhaustionRow, err := querier.LockReplayReserveExhaustionForOperatorReserve(ctx,
		sqlc.LockReplayReserveExhaustionForOperatorReserveParams{ExhaustionCommandID: row.ExhaustionCommandID,
			TournamentID: row.TournamentID, OldWaveID: row.OldWaveID, SeriesID: row.SeriesID,
			SlotID: row.SlotID, AssignmentID: row.AssignmentID})
	if err != nil {
		return nil, replayWorkflowLookupError("reload reserve exhaustion", err)
	}
	exhaustion, err := source.replayExhaustion(exhaustionRow)
	if err != nil {
		return nil, err
	}
	authority, err := r.loadReserveAuthority(ctx, querier, source, command)
	if err != nil {
		return nil, err
	}
	reserve, err := assignmentusecase.BuildReserveAssignmentRecord(
		operatorReserveAssignmentCommand(command, row.PromotedAt.Time.Round(0).UTC()), authority,
	)
	if err != nil || reserve.ProofDigest != copyReplayDigest(row.ProofDigest) || reserve.ContentDigest != copyReplayDigest(row.ContentDigest) {
		return nil, errReplayWorkflowAuthority
	}
	series, err := source.seriesExecution(domain.SeriesStateReplayRequired, nil)
	if err != nil {
		return nil, err
	}
	record := gameusecase.OperatorReserve{Scope: replayScopeFromCommand(command), CommandID: row.CommandID,
		ExpectedAuthorityRevision: row.SourceSeriesRevision, Exhaustion: exhaustion, Reserve: reserve, Series: series}
	if record.Validate() != nil {
		return nil, errReplayWorkflowAuthority
	}
	return &record, nil
}

func (r *TournamentAdminReplayPostgres) reconcileStoredOperatorReserve(
	ctx context.Context,
	querier *sqlc.Queries,
	command tournamentadmin.ReserveCommand,
	requestDigest [sha256.Size]byte,
	row sqlc.OperatorReplayReserve,
) (*gameusecase.OperatorReserve, bool, error) {
	if !bytesEqual(row.RequestDigest, requestDigest[:]) {
		return nil, false, gameusecase.ErrOperatorReserveReuse
	}
	source, err := r.loadSource(ctx, querier, replaySourceScope{TournamentID: command.TournamentID,
		OldWaveID: command.OldWaveID, SeriesID: command.SeriesID, SlotID: command.SlotID,
		FailedGameID: command.AssignmentAttemptID, AssignmentID: command.AssignmentID})
	if err != nil {
		return nil, false, err
	}
	record, err := r.rehydrateOperatorReserve(ctx, querier, source, command, row)
	if err != nil {
		return nil, false, err
	}
	return record, false, nil
}

func operatorReserveDocumentFromRecord(
	command tournamentadmin.ReserveCommand,
	record gameusecase.OperatorReserve,
) operatorReserveDocument {
	return operatorReserveDocument{CommandID: command.CommandID, Scope: replayDocumentScope{
		TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
		SlotID: command.SlotID, AssignmentID: command.AssignmentID},
		ExpectedExhaustionCommandID: command.ExpectedExhaustionCommandID,
		ExpectedAuthorityRevision:   record.ExpectedAuthorityRevision, AssignmentAttemptID: command.AssignmentAttemptID,
		FailedGameID: command.AssignmentAttemptID, ClosureRevisionID: record.Exhaustion.ClosureRevisionID.UUID(),
		FromSnapshotID: record.Reserve.FromSnapshotID, ProposedTaskID: record.Reserve.Snapshot.TaskID,
		ProposedVersion: record.Reserve.Snapshot.Version, ProposedSnapshotID: record.Reserve.Snapshot.SnapshotID,
		EvidenceID: record.Reserve.Evidence.ID, ActorID: command.Operator.ActorID, Reason: command.Reason,
		PromotedAt: record.Reserve.PromotedAt}
}

func replayReserveCommandDigest(command tournamentadmin.ReserveCommand) []byte {
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	payload, err := json.Marshal(struct {
		Action string                         `json:"action"`
		Value  tournamentadmin.ReserveCommand `json:"command"`
	}{Action: "operator_reserve", Value: command})
	if err != nil {
		return nil
	}
	digest := sha256.Sum256(payload)
	return digest[:]
}

func copyReplayDigest(value []byte) [sha256.Size]byte {
	var result [sha256.Size]byte
	if len(value) == len(result) {
		copy(result[:], value)
	}
	return result
}

func (r *TournamentAdminReplayPostgres) loadReplayReserveChain(
	ctx context.Context,
	querier *sqlc.Queries,
	source replaySource,
	operatorCommandID *uuid.UUID,
) (gameusecase.ReplayReserveChain, []sqlc.TaskVersionReservation, error) {
	rows, err := querier.LockReplayWorkflowReserveChain(ctx, sqlc.LockReplayWorkflowReserveChainParams{
		PlanID: source.row.AssignmentPlan.ID, BranchID: source.row.AssignmentBranch.ID,
	})
	if err != nil {
		return gameusecase.ReplayReserveChain{}, nil, replayWorkflowLookupError("lock replay reserve chain", err)
	}
	chain, reservations, err := replayWorkflowReserveChain(source.row.Assignment.ID, source.row.TaskSnapshot.ID, rows, operatorCommandID)
	if err != nil {
		return gameusecase.ReplayReserveChain{}, nil, err
	}
	return chain, reservations, nil
}

func replayReplacementDocumentFromReplacement(
	command tournamentadmin.ReplayCommand,
	replacement gameusecase.ReplayReplacement,
) replayReplacementDocument {
	return replayReplacementDocument{CommandID: command.CommandID, Scope: replayDocumentScope{
		TournamentID: command.TournamentID, OldWaveID: command.OldWaveID, SeriesID: command.SeriesID,
		SlotID: command.SlotID, AssignmentID: command.AssignmentID},
		ExpectedAuthorityRevision: replacement.ExpectedAuthorityRevision,
		AssignmentAttemptID:       replacement.AssignmentAttemptID, FailedGameID: command.FailedGameID,
		ClosureRevisionID: replacement.ClosureRevisionID.UUID(), FromSnapshotID: replacement.FromSnapshotID,
		ReservePosition: replacement.ReservePosition, SnapshotID: replacement.Snapshot.SnapshotID,
		ReplacementGameID: replacement.Game.ID, ReplacementWaveID: replacement.Wave.ID,
		WaveRevisionID: replacement.Wave.RevisionID.UUID(), ReadyWindowID: replacement.Wave.ReadyWindow.ID,
		ReadyWindowRevisionID: replacement.Wave.ReadyWindow.RevisionID.UUID(), ActorID: command.Operator.ActorID,
		Reason: command.Reason, OpenedAt: replacement.OpenedAt}
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func replayReplacementFromStored(
	source replaySource,
	command tournamentadmin.ReplayCommand,
	row sqlc.ReplayReplacement,
	chain gameusecase.ReplayReserveChain,
) (*gameusecase.ReplayReplacement, bool, error) {
	document, err := decodeReplayStorageDocument[replayReplacementDocument](row.RecordDocument)
	if err != nil || validateReplayReplacementDocument(document, row) != nil {
		return nil, false, errReplayWorkflowAuthority
	}
	if row.CommandID != command.CommandID || row.TournamentID != command.TournamentID ||
		row.OldWaveID != command.OldWaveID || row.SeriesID != command.SeriesID || row.SlotID != command.SlotID ||
		row.AssignmentID != command.AssignmentID || row.FailedGameID != command.FailedGameID ||
		row.ClosureRevisionID != command.ExpectedClosureRevisionID || row.ReplacementAssignmentAttemptID != command.AssignmentAttemptID ||
		row.ReplacementGameID != command.ReplacementGameID || row.ReplacementWaveID != command.ReplacementWaveID ||
		row.ReplacementWaveRevisionID != command.ReplacementWaveRevisionID || row.ReadyWindowID != command.ReadyWindowID ||
		row.ReadyWindowRevisionID != command.ReadyWindowRevisionID || !row.OpenedAt.Valid {
		return nil, false, gameusecase.ErrReplayReplacementReuse
	}
	index := int(row.ReservePosition) - 1
	if index < 0 || index >= len(chain.Snapshots) || chain.Snapshots[index].SnapshotID != row.SnapshotID {
		return nil, false, errReplayWorkflowAuthority
	}
	failed, found := replayFindGameFromSlot(source, row.FailedGameID)
	if !found || failed.State != domain.GameStateVoid {
		return nil, false, errReplayWorkflowAuthority
	}
	openedAt := row.OpenedAt.Time.Round(0).UTC()
	deadline := openedAt.Add(domain.ReadyWindowDuration)
	game := domain.Game{ID: row.ReplacementGameID, SlotID: source.row.GameSlot.ID,
		AttemptNo: failed.AttemptNo + 1, State: domain.GameStatePlanned}
	slot := domain.GameSlot{ID: source.row.GameSlot.ID, SeriesID: source.row.Series.ID,
		Position: int(source.row.GameSlot.SlotNumber), Category: domain.Category(source.row.GameSlot.Category),
		ScoreBefore: domain.SeriesScore{FirstParticipantWins: int(source.row.GameSlot.FirstParticipantWinsBefore),
			SecondParticipantWins: int(source.row.GameSlot.SecondParticipantWinsBefore)}, Attempts: []domain.Game{failed, game}}
	wave := domain.Wave{ID: row.ReplacementWaveID, TournamentID: row.TournamentID,
		RevisionID: domain.WaveRevisionID(row.ReplacementWaveRevisionID), State: domain.WaveStateReadyWindowOpen,
		Members: []domain.WaveMember{{ParticipantID: source.row.Series.FirstParticipantID}, {ParticipantID: source.row.Series.SecondParticipantID}},
		ReadyWindow: &domain.ReadyWindow{ID: row.ReadyWindowID, WaveID: row.ReplacementWaveID,
			RevisionID: domain.ReadyWindowRevisionID(row.ReadyWindowRevisionID), State: domain.ReadyWindowStateOpen,
			OpenedAt: openedAt, Deadline: deadline}}
	replacement := gameusecase.ReplayReplacement{Scope: replayScopeFromReplayCommand(command), CommandID: row.CommandID,
		ExpectedAuthorityRevision: row.SourceSeriesRevision, ClosureRevisionID: domain.WaveRevisionID(row.ClosureRevisionID),
		FromSnapshotID: row.FromSnapshotID, AssignmentAttemptID: row.ReplacementAssignmentAttemptID,
		ReservePosition: int(row.ReservePosition), Snapshot: chain.Snapshots[index], Category: chain.Snapshots[index].Category,
		Slot: slot, Game: game, Wave: wave, OpenedAt: openedAt}
	if replacement.Validate() != nil {
		return nil, false, errReplayWorkflowAuthority
	}
	return &replacement, false, nil
}

func replayFindGameFromSlot(source replaySource, gameID uuid.UUID) (domain.Game, bool) {
	for _, row := range source.graph {
		if row.GameAttempt.ID != gameID {
			continue
		}
		game, err := replayGameFromRow(row.GameAttempt, mapReplayHeads(source.gameHeads))
		if err != nil {
			return domain.Game{}, false
		}
		return game, true
	}
	return domain.Game{}, false
}

func mapReplayHeads(rows []sqlc.LockReplayWorkflowGameResultHeadsRow) map[uuid.UUID]sqlc.LockReplayWorkflowGameResultHeadsRow {
	result := make(map[uuid.UUID]sqlc.LockReplayWorkflowGameResultHeadsRow, len(rows))
	for _, row := range rows {
		result[row.GameAttemptID] = row
	}
	return result
}

func sameReplayParticipants(ids []uuid.UUID, members []domain.WaveMember) bool {
	if len(ids) != len(members) {
		return false
	}
	found := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return false
		}
		found[id] = struct{}{}
	}
	if len(found) != len(members) {
		return false
	}
	for _, member := range members {
		if _, ok := found[member.ParticipantID]; !ok {
			return false
		}
	}
	return true
}
