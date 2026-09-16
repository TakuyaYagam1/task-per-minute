package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
)

const (
	goldenRepositorySchema        = "golden-aggregate-v1"
	goldenRepositoryAggregateKind = "state"
)

type GoldenRepositoryStateCommandKind string

const (
	GoldenRepositoryStateReady      GoldenRepositoryStateCommandKind = "state_ready"
	GoldenRepositoryStateNoShow     GoldenRepositoryStateCommandKind = "state_no_show"
	GoldenRepositoryStateAllocation GoldenRepositoryStateCommandKind = "state_allocation"
)

var (
	ErrGoldenRepositoryInvalid      = errors.New("golden repository: invalid durable record")
	ErrGoldenRepositoryConflict     = errors.New("golden repository: current revision conflict")
	ErrGoldenRepositoryCommandReuse = errors.New("golden repository: command identifier was reused")
)

// GoldenRepositoryScope identifies one typed Golden group revision state. The
// database foreign key binds RosterID and TournamentID to GroupRevisionID;
// topology snapshot validation independently binds its tournament and group.
type GoldenRepositoryScope struct {
	TournamentID    uuid.UUID
	RosterID        uuid.UUID
	GroupID         uuid.UUID
	GroupRevisionID uuid.UUID
}

type GoldenRepositoryRevision struct {
	ID         uuid.UUID
	Number     int64
	PreviousID *uuid.UUID
	Snapshot   goldenusecase.RevisionSnapshot
	Digest     [sha256.Size]byte
	CreatedAt  time.Time
}

type goldenRepositorySnapshotDocument struct {
	Purpose                            goldenusecase.GroupRevisionPurpose `json:"purpose"`
	Effect                             goldenusecase.GroupRevisionEffect  `json:"effect"`
	TournamentID                       uuid.UUID                          `json:"tournament_id"`
	GroupID                            uuid.UUID                          `json:"group_id"`
	RevisionID                         domain.DerivedRevisionID           `json:"revision_id"`
	RevisionNo                         int                                `json:"revision_no"`
	PreviousRevisionID                 *domain.DerivedRevisionID          `json:"previous_revision_id,omitempty"`
	SourceProjectionID                 uuid.UUID                          `json:"source_projection_id"`
	SourceProjectionRevisionID         domain.DerivedRevisionID           `json:"source_projection_revision_id"`
	SourceProjectionRevisionNo         int                                `json:"source_projection_revision_no"`
	SourceProjectionPreviousRevisionID *domain.DerivedRevisionID          `json:"source_projection_previous_revision_id,omitempty"`
	SourceProjectionPayloadDigest      [sha256.Size]byte                  `json:"source_projection_payload_digest"`
	PositionFrom                       int                                `json:"position_from"`
	PositionTo                         int                                `json:"position_to"`
	Members                            []goldenRepositoryMemberDocument   `json:"members"`
	Payload                            []byte                             `json:"payload"`
	PayloadDigest                      [sha256.Size]byte                  `json:"payload_digest"`
	ProofHash                          string                             `json:"proof_hash"`
}

type goldenRepositoryMemberDocument struct {
	ParticipantID     uuid.UUID      `json:"participant_id"`
	Points            int            `json:"points"`
	Buchholz          int            `json:"buchholz"`
	HeadToHeadPoints  int            `json:"head_to_head_points"`
	HeadToHeadApplied bool           `json:"head_to_head_applied"`
	EffectiveTime     time.Duration  `json:"effective_time"`
	AcceptedSolveTime *time.Duration `json:"accepted_solve_time,omitempty"`
	Seed              int            `json:"seed"`
}

type GoldenRepositoryCommand struct {
	ID         uuid.UUID
	Kind       GoldenRepositoryStateCommandKind
	Digest     [sha256.Size]byte
	OccurredAt time.Time
}

type GoldenRepositoryCommit struct {
	Scope    GoldenRepositoryScope
	Revision GoldenRepositoryRevision
	Command  *GoldenRepositoryCommand
}

type GoldenRepositoryStore interface {
	FindCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenRepositoryCommit, error)
	Commit(ctx context.Context, commit GoldenRepositoryCommit) (*GoldenRepositoryRevision, bool, error)
}

type GoldenRepositoryStorePostgres struct {
	tx *db.TxManager
}

var _ GoldenRepositoryStore = (*GoldenRepositoryStorePostgres)(nil)

func NewGoldenRepositoryStorePostgres(tx *db.TxManager) *GoldenRepositoryStorePostgres {
	return &GoldenRepositoryStorePostgres{tx: tx}
}

func (repository *GoldenRepositoryStorePostgres) FindCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*GoldenRepositoryCommit, error) {
	if repository == nil || repository.tx == nil || ctx == nil || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, ErrGoldenRepositoryInvalid
	}
	row, err := repository.tx.Querier(ctx).FindGoldenRepositoryCommand(
		ctx,
		sqlc.FindGoldenRepositoryCommandParams{TournamentID: tournamentID, CommandID: commandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GoldenRepositoryStorePostgres - FindCommand: %w", err)
	}
	return repository.mapGoldenRepositoryCommand(ctx, row)
}

func (repository *GoldenRepositoryStorePostgres) Commit(
	ctx context.Context,
	commit GoldenRepositoryCommit,
) (*GoldenRepositoryRevision, bool, error) {
	if repository == nil || repository.tx == nil || ctx == nil || commit.validate() != nil {
		return nil, false, ErrGoldenRepositoryInvalid
	}

	var (
		stored  *GoldenRepositoryRevision
		changed bool
	)
	if err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		stored, changed, err = repository.commit(txCtx, commit)
		return err
	}); err != nil {
		return nil, false, err
	}
	if stored == nil || stored.validate() != nil {
		return nil, false, fmt.Errorf("GoldenRepositoryStorePostgres - Commit: %w", ErrGoldenRepositoryInvalid)
	}
	return cloneGoldenRepositoryRevision(*stored), changed, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *GoldenRepositoryStorePostgres) commit(
	ctx context.Context,
	commit GoldenRepositoryCommit,
) (*GoldenRepositoryRevision, bool, error) {
	querier := repository.tx.Querier(ctx)
	// The random identifier is a storage identity only. The unique natural key
	// below is the authority-bound scope and wins concurrent creation races.
	_, err := querier.CreateGoldenRepositoryScope(ctx, goldenRepositoryScopeCreateParams(commit.Scope, uuid.New(), commit.Revision.CreatedAt))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, goldenRepositoryWriteError("GoldenRepositoryStorePostgres - create scope", err)
	}

	// This is the first and only adapter-level lock. Callers lock normalized
	// authority before entering this store, so no storage identity grants scope.
	scope, err := querier.LockGoldenRepositoryScope(ctx, goldenRepositoryScopeLockParams(commit.Scope))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrGoldenRepositoryConflict
	}
	if err != nil {
		return nil, false, fmt.Errorf("GoldenRepositoryStorePostgres - lock scope: %w", err)
	}
	if !goldenRepositoryScopeMatches(scope, commit.Scope) ||
		!goldenRepositoryScopeContainsRevision(commit.Scope, commit.Revision) {
		return nil, false, fmt.Errorf("GoldenRepositoryStorePostgres - lock scope: %w", ErrGoldenRepositoryInvalid)
	}

	// Replay is deliberately checked before loading or advancing the current
	// head. A reused command never becomes a stale-CAS error.
	if commit.Command != nil {
		replay, replayErr := repository.FindCommand(ctx, commit.Scope.TournamentID, commit.Command.ID)
		if replayErr != nil {
			return nil, false, replayErr
		}
		if replay != nil {
			if !goldenRepositoryReplayMatches(*replay, commit) {
				return nil, false, ErrGoldenRepositoryCommandReuse
			}
			return replayRevision(*replay), false, nil
		}
	}

	head, err := querier.LockGoldenRepositoryHead(ctx, scope.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("GoldenRepositoryStorePostgres - lock head: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if commit.Revision.Number != 1 || commit.Revision.PreviousID != nil {
			return nil, false, ErrGoldenRepositoryConflict
		}
	} else if !goldenRepositoryRevisionFollowsHead(commit.Revision, head) {
		return nil, false, ErrGoldenRepositoryConflict
	}

	payload, err := canonicalGoldenRepositoryEnvelope(commit.Revision)
	if err != nil {
		return nil, false, err
	}
	created, err := querier.CreateGoldenRepositoryRevision(
		ctx,
		goldenRepositoryRevisionCreateParams(scope.ID, commit.Revision, payload),
	)
	if err != nil {
		return nil, false, goldenRepositoryWriteError("GoldenRepositoryStorePostgres - create revision", err)
	}
	stored, err := mapGoldenRepositoryRevision(created)
	if err != nil {
		return nil, false, err
	}
	if !goldenRepositoryRevisionEqual(*stored, commit.Revision) {
		return nil, false, fmt.Errorf("GoldenRepositoryStorePostgres - create revision readback: %w", ErrGoldenRepositoryInvalid)
	}

	if head.ScopeID == uuid.Nil {
		_, err = querier.CreateGoldenRepositoryHead(ctx, goldenRepositoryHeadCreateParams(scope.ID, commit.Revision))
	} else {
		_, err = querier.AdvanceGoldenRepositoryHeadCAS(
			ctx,
			goldenRepositoryHeadAdvanceParams(scope.ID, head, commit.Revision),
		)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrGoldenRepositoryConflict
	}
	if err != nil {
		return nil, false, goldenRepositoryWriteError("GoldenRepositoryStorePostgres - advance head", err)
	}

	if commit.Command != nil {
		journal, journalErr := querier.CreateGoldenRepositoryCommand(
			ctx,
			goldenRepositoryCommandCreateParams(scope.ID, commit.Scope.TournamentID, commit.Revision.ID, *commit.Command),
		)
		if journalErr != nil {
			return nil, false, goldenRepositoryWriteError("GoldenRepositoryStorePostgres - create command", journalErr)
		}
		if !goldenRepositoryCommandMatches(journal, scope.ID, commit.Scope.TournamentID, commit.Revision.ID, *commit.Command) {
			return nil, false, fmt.Errorf("GoldenRepositoryStorePostgres - command readback: %w", ErrGoldenRepositoryInvalid)
		}
	}

	readback, err := querier.LoadGoldenRepositoryHead(ctx, scope.ID)
	if err != nil {
		return nil, false, fmt.Errorf("GoldenRepositoryStorePostgres - read head: %w", err)
	}
	if !goldenRepositoryHeadMatches(readback, *stored) {
		return nil, false, fmt.Errorf("GoldenRepositoryStorePostgres - head readback: %w", ErrGoldenRepositoryInvalid)
	}
	return stored, true, nil
}

func (commit GoldenRepositoryCommit) validate() error {
	if err := commit.Scope.validate(); err != nil {
		return err
	}
	if err := commit.Revision.validate(); err != nil {
		return err
	}
	if !goldenRepositoryScopeContainsRevision(commit.Scope, commit.Revision) {
		return ErrGoldenRepositoryInvalid
	}
	if commit.Command != nil {
		if err := commit.Command.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (scope GoldenRepositoryScope) validate() error {
	if scope.TournamentID == uuid.Nil || scope.RosterID == uuid.Nil || scope.GroupID == uuid.Nil ||
		scope.GroupRevisionID == uuid.Nil {
		return ErrGoldenRepositoryInvalid
	}
	return nil
}

func (revision GoldenRepositoryRevision) validate() error {
	if revision.ID == uuid.Nil || revision.Number < 1 || !domain.IsValidServerTime(revision.CreatedAt) ||
		isZeroGoldenRepositoryDigest(revision.Digest) {
		return ErrGoldenRepositoryInvalid
	}
	if (revision.Number == 1) != (revision.PreviousID == nil) {
		return ErrGoldenRepositoryInvalid
	}
	if revision.PreviousID != nil && *revision.PreviousID == uuid.Nil {
		return ErrGoldenRepositoryInvalid
	}
	canonical, err := goldenRepositoryCanonicalSnapshot(revision.Snapshot)
	if err != nil || revision.Digest != canonical.PayloadDigest {
		return ErrGoldenRepositoryInvalid
	}
	return nil
}

func (command GoldenRepositoryCommand) validate() error {
	if command.ID == uuid.Nil || !command.Kind.valid() ||
		isZeroGoldenRepositoryDigest(command.Digest) || !domain.IsValidServerTime(command.OccurredAt) {
		return ErrGoldenRepositoryInvalid
	}
	return nil
}

func canonicalGoldenRepositoryEnvelope(revision GoldenRepositoryRevision) ([]byte, error) {
	canonical, err := goldenRepositoryCanonicalSnapshot(revision.Snapshot)
	if err != nil || revision.Digest != canonical.PayloadDigest {
		return nil, ErrGoldenRepositoryInvalid
	}
	payload, err := json.Marshal(struct {
		Schema         string                           `json:"schema"`
		Kind           string                           `json:"kind"`
		RevisionID     uuid.UUID                        `json:"revision_id"`
		RevisionNumber int64                            `json:"revision_number"`
		PayloadDigest  string                           `json:"payload_digest"`
		Document       goldenRepositorySnapshotDocument `json:"document"`
	}{
		Schema:         goldenRepositorySchema,
		Kind:           goldenRepositoryAggregateKind,
		RevisionID:     revision.ID,
		RevisionNumber: revision.Number,
		PayloadDigest:  hex.EncodeToString(revision.Digest[:]),
		Document:       goldenRepositorySnapshotToDocument(canonical),
	})
	if err != nil {
		return nil, fmt.Errorf("GoldenRepositoryStorePostgres - encode envelope: %w", err)
	}
	return payload, nil
}

func goldenRepositoryCanonicalSnapshot(snapshot goldenusecase.RevisionSnapshot) (goldenusecase.RevisionSnapshot, error) {
	restored, err := goldenusecase.RestoreGroupRevision(snapshot)
	if err != nil {
		return goldenusecase.RevisionSnapshot{}, fmt.Errorf("%w: topology snapshot", ErrGoldenRepositoryInvalid)
	}
	return restored.PersistenceSnapshot(), nil
}

func goldenRepositorySnapshotToDocument(snapshot goldenusecase.RevisionSnapshot) goldenRepositorySnapshotDocument {
	document := goldenRepositorySnapshotDocument{
		Purpose: snapshot.Purpose, Effect: snapshot.Effect, TournamentID: snapshot.TournamentID, GroupID: snapshot.GroupID,
		RevisionID: snapshot.RevisionID, RevisionNo: snapshot.RevisionNo,
		PreviousRevisionID: cloneGoldenRepositoryDerivedRevisionID(snapshot.PreviousRevisionID),
		SourceProjectionID: snapshot.SourceProjectionID, SourceProjectionRevisionID: snapshot.SourceProjectionRevisionID,
		SourceProjectionRevisionNo:         snapshot.SourceProjectionRevisionNo,
		SourceProjectionPreviousRevisionID: cloneGoldenRepositoryDerivedRevisionID(snapshot.SourceProjectionPreviousRevisionID),
		SourceProjectionPayloadDigest:      snapshot.SourceProjectionPayloadDigest, PositionFrom: snapshot.PositionFrom,
		PositionTo: snapshot.PositionTo, Payload: append([]byte(nil), snapshot.Payload...),
		PayloadDigest: snapshot.PayloadDigest, ProofHash: snapshot.ProofHash,
		Members: make([]goldenRepositoryMemberDocument, len(snapshot.Members)),
	}
	for index, member := range snapshot.Members {
		document.Members[index] = goldenRepositoryMemberDocument{
			ParticipantID: member.ParticipantID, Points: member.Points, Buchholz: member.Buchholz,
			HeadToHeadPoints: member.HeadToHeadPoints, HeadToHeadApplied: member.HeadToHeadApplied,
			EffectiveTime: member.EffectiveTime, AcceptedSolveTime: cloneGoldenRepositoryDuration(member.AcceptedSolveTime),
			Seed: member.Seed,
		}
	}
	return document
}

func (document goldenRepositorySnapshotDocument) snapshot() goldenusecase.RevisionSnapshot {
	snapshot := goldenusecase.RevisionSnapshot{
		Purpose: document.Purpose, Effect: document.Effect, TournamentID: document.TournamentID, GroupID: document.GroupID,
		RevisionID: document.RevisionID, RevisionNo: document.RevisionNo,
		PreviousRevisionID: cloneGoldenRepositoryDerivedRevisionID(document.PreviousRevisionID),
		SourceProjectionID: document.SourceProjectionID, SourceProjectionRevisionID: document.SourceProjectionRevisionID,
		SourceProjectionRevisionNo:         document.SourceProjectionRevisionNo,
		SourceProjectionPreviousRevisionID: cloneGoldenRepositoryDerivedRevisionID(document.SourceProjectionPreviousRevisionID),
		SourceProjectionPayloadDigest:      document.SourceProjectionPayloadDigest, PositionFrom: document.PositionFrom,
		PositionTo: document.PositionTo, Payload: append([]byte(nil), document.Payload...),
		PayloadDigest: document.PayloadDigest, ProofHash: document.ProofHash,
		Members: make([]goldenusecase.GroupMemberSeed, len(document.Members)),
	}
	for index, member := range document.Members {
		snapshot.Members[index] = goldenusecase.GroupMemberSeed{
			ParticipantID: member.ParticipantID, Points: member.Points, Buchholz: member.Buchholz,
			HeadToHeadPoints: member.HeadToHeadPoints, HeadToHeadApplied: member.HeadToHeadApplied,
			EffectiveTime: member.EffectiveTime, AcceptedSolveTime: cloneGoldenRepositoryDuration(member.AcceptedSolveTime),
			Seed: member.Seed,
		}
	}
	return snapshot
}

func goldenRepositoryScopeCreateParams(
	scope GoldenRepositoryScope,
	id uuid.UUID,
	createdAt time.Time,
) sqlc.CreateGoldenRepositoryScopeParams {
	return sqlc.CreateGoldenRepositoryScopeParams{
		ID: id, AggregateKind: goldenRepositoryAggregateKind, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		GroupID:         uuid.NullUUID{UUID: scope.GroupID, Valid: true},
		GroupRevisionID: uuid.NullUUID{UUID: scope.GroupRevisionID, Valid: true}, CreatedAt: goldenRepositoryTimestamptz(createdAt),
	}
}

func goldenRepositoryScopeLockParams(scope GoldenRepositoryScope) sqlc.LockGoldenRepositoryScopeParams {
	return sqlc.LockGoldenRepositoryScopeParams{
		AggregateKind: goldenRepositoryAggregateKind, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		GroupID:         uuid.NullUUID{UUID: scope.GroupID, Valid: true},
		GroupRevisionID: uuid.NullUUID{UUID: scope.GroupRevisionID, Valid: true},
	}
}

func goldenRepositoryRevisionCreateParams(
	scopeID uuid.UUID,
	revision GoldenRepositoryRevision,
	payload []byte,
) sqlc.CreateGoldenRepositoryRevisionParams {
	return sqlc.CreateGoldenRepositoryRevisionParams{
		ScopeID: scopeID, AggregateKind: goldenRepositoryAggregateKind, RevisionID: revision.ID, RevisionNumber: revision.Number,
		PreviousRevisionID: goldenRepositoryNullableUUID(revision.PreviousID), Payload: payload,
		PayloadDigest: append([]byte(nil), revision.Digest[:]...), CreatedAt: goldenRepositoryTimestamptz(revision.CreatedAt),
	}
}

func goldenRepositoryHeadCreateParams(
	scopeID uuid.UUID,
	revision GoldenRepositoryRevision,
) sqlc.CreateGoldenRepositoryHeadParams {
	return sqlc.CreateGoldenRepositoryHeadParams{
		ScopeID: scopeID, RevisionID: revision.ID, RevisionNumber: revision.Number,
		PayloadDigest: append([]byte(nil), revision.Digest[:]...), UpdatedAt: goldenRepositoryTimestamptz(revision.CreatedAt),
	}
}

func goldenRepositoryHeadAdvanceParams(
	scopeID uuid.UUID,
	head sqlc.LockGoldenRepositoryHeadRow,
	revision GoldenRepositoryRevision,
) sqlc.AdvanceGoldenRepositoryHeadCASParams {
	return sqlc.AdvanceGoldenRepositoryHeadCASParams{
		NextRevisionID: revision.ID, NextRevisionNumber: revision.Number,
		NextPayloadDigest: append([]byte(nil), revision.Digest[:]...), UpdatedAt: goldenRepositoryTimestamptz(revision.CreatedAt),
		ScopeID: scopeID, ExpectedRevisionID: head.RevisionID, ExpectedRevisionNumber: head.RevisionNumber,
		ExpectedPayloadDigest: append([]byte(nil), head.PayloadDigest...),
	}
}

func goldenRepositoryCommandCreateParams(
	scopeID, tournamentID, revisionID uuid.UUID,
	command GoldenRepositoryCommand,
) sqlc.CreateGoldenRepositoryCommandParams {
	return sqlc.CreateGoldenRepositoryCommandParams{
		ScopeID: scopeID, TournamentID: tournamentID, CommandID: command.ID, CommandKind: string(command.Kind),
		CommandDigest: append([]byte(nil), command.Digest[:]...), ResultRevisionID: revisionID,
		OccurredAt: goldenRepositoryTimestamptz(command.OccurredAt), CreatedAt: goldenRepositoryTimestamptz(command.OccurredAt),
	}
}

func (repository *GoldenRepositoryStorePostgres) mapGoldenRepositoryCommand(
	ctx context.Context,
	row sqlc.FindGoldenRepositoryCommandRow,
) (*GoldenRepositoryCommit, error) {
	scope, err := repository.scopeFromID(ctx, row.ScopeID)
	if err != nil || scope == nil || scope.TournamentID != row.TournamentID || row.AggregateKind != goldenRepositoryAggregateKind {
		return nil, fmt.Errorf("GoldenRepositoryStorePostgres - map command scope: %w", ErrGoldenRepositoryInvalid)
	}
	revision, err := goldenRepositoryRevisionFromEnvelope(
		row.ResultRevisionID,
		row.RevisionNumber,
		goldenRepositoryUUIDPointer(row.PreviousRevisionID),
		row.Payload,
		row.PayloadDigest,
		row.CreatedAt.Time,
	)
	if err != nil {
		return nil, err
	}
	if !goldenRepositoryScopeContainsRevision(*scope, revision) {
		return nil, fmt.Errorf("GoldenRepositoryStorePostgres - map command scope revision: %w", ErrGoldenRepositoryInvalid)
	}
	command, err := goldenRepositoryCommandFromRow(row)
	if err != nil {
		return nil, err
	}
	return &GoldenRepositoryCommit{Scope: *scope, Revision: revision, Command: &command}, nil
}

func (repository *GoldenRepositoryStorePostgres) scopeFromID(
	ctx context.Context,
	id uuid.UUID,
) (*GoldenRepositoryScope, error) {
	if repository == nil || repository.tx == nil || ctx == nil || id == uuid.Nil {
		return nil, ErrGoldenRepositoryInvalid
	}
	row, err := repository.tx.Querier(ctx).LoadGoldenRepositoryScope(ctx, id)
	if err != nil {
		return nil, err
	}
	return goldenRepositoryScopeFromRow(row)
}

func mapGoldenRepositoryRevision(row sqlc.GoldenRepositoryRevision) (*GoldenRepositoryRevision, error) {
	revision, err := goldenRepositoryRevisionFromEnvelope(
		row.RevisionID,
		row.RevisionNumber,
		goldenRepositoryUUIDPointer(row.PreviousRevisionID),
		row.Payload,
		row.PayloadDigest,
		row.CreatedAt.Time,
	)
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

func goldenRepositoryRevisionFromEnvelope(
	id uuid.UUID,
	number int64,
	previousID *uuid.UUID,
	payload []byte,
	digest []byte,
	createdAt time.Time,
) (GoldenRepositoryRevision, error) {
	var envelope struct {
		Schema         string                           `json:"schema"`
		Kind           string                           `json:"kind"`
		RevisionID     uuid.UUID                        `json:"revision_id"`
		RevisionNumber int64                            `json:"revision_number"`
		PayloadDigest  string                           `json:"payload_digest"`
		Document       goldenRepositorySnapshotDocument `json:"document"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Schema != goldenRepositorySchema ||
		envelope.Kind != goldenRepositoryAggregateKind || envelope.RevisionID != id || envelope.RevisionNumber != number {
		return GoldenRepositoryRevision{}, fmt.Errorf("GoldenRepositoryStorePostgres - decode revision: %w", ErrGoldenRepositoryInvalid)
	}
	if len(digest) != sha256.Size || envelope.PayloadDigest != hex.EncodeToString(digest) {
		return GoldenRepositoryRevision{}, fmt.Errorf("GoldenRepositoryStorePostgres - decode revision digest: %w", ErrGoldenRepositoryInvalid)
	}
	decoded := envelope.Document.snapshot()
	canonical, err := goldenRepositoryCanonicalSnapshot(decoded)
	if err != nil || !reflect.DeepEqual(decoded, canonical) {
		return GoldenRepositoryRevision{}, fmt.Errorf("GoldenRepositoryStorePostgres - decode revision snapshot: %w", ErrGoldenRepositoryInvalid)
	}
	revision := GoldenRepositoryRevision{ID: id, Number: number, PreviousID: cloneUUIDPointer(previousID), Snapshot: canonical, CreatedAt: createdAt}
	copy(revision.Digest[:], digest)
	if revision.validate() != nil || revision.Digest != canonical.PayloadDigest {
		return GoldenRepositoryRevision{}, fmt.Errorf("GoldenRepositoryStorePostgres - validate revision: %w", ErrGoldenRepositoryInvalid)
	}
	return revision, nil
}

func goldenRepositoryScopeFromRow(row sqlc.GoldenRepositoryScope) (*GoldenRepositoryScope, error) {
	if row.AggregateKind != goldenRepositoryAggregateKind || row.PlanSetID.Valid || !row.GroupID.Valid ||
		!row.GroupRevisionID.Valid || row.AttemptID.Valid || row.WaveID.Valid || row.AssignmentID.Valid ||
		row.SnapshotID.Valid || row.TaskID.Valid {
		return nil, ErrGoldenRepositoryInvalid
	}
	scope := &GoldenRepositoryScope{
		TournamentID: row.TournamentID, RosterID: row.RosterID,
		GroupID: row.GroupID.UUID, GroupRevisionID: row.GroupRevisionID.UUID,
	}
	return scope, scope.validate()
}

func goldenRepositoryCommandFromRow(row sqlc.FindGoldenRepositoryCommandRow) (GoldenRepositoryCommand, error) {
	command := GoldenRepositoryCommand{ID: row.CommandID, Kind: GoldenRepositoryStateCommandKind(row.CommandKind), OccurredAt: row.OccurredAt.Time}
	if len(row.CommandDigest) != sha256.Size {
		return GoldenRepositoryCommand{}, ErrGoldenRepositoryInvalid
	}
	copy(command.Digest[:], row.CommandDigest)
	if row.AggregateKind != goldenRepositoryAggregateKind || command.validate() != nil {
		return GoldenRepositoryCommand{}, ErrGoldenRepositoryInvalid
	}
	return command, nil
}

func goldenRepositoryScopeMatches(row sqlc.GoldenRepositoryScope, scope GoldenRepositoryScope) bool {
	stored, err := goldenRepositoryScopeFromRow(row)
	return err == nil && goldenRepositoryScopesEqual(*stored, scope)
}

func goldenRepositoryScopesEqual(first, second GoldenRepositoryScope) bool {
	return first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.GroupID == second.GroupID && first.GroupRevisionID == second.GroupRevisionID
}

func goldenRepositoryScopeContainsRevision(scope GoldenRepositoryScope, revision GoldenRepositoryRevision) bool {
	canonical, err := goldenRepositoryCanonicalSnapshot(revision.Snapshot)
	return err == nil && scope.TournamentID == canonical.TournamentID && scope.GroupID == canonical.GroupID &&
		scope.GroupRevisionID == canonical.RevisionID.UUID()
}

func goldenRepositoryRevisionFollowsHead(revision GoldenRepositoryRevision, head sqlc.LockGoldenRepositoryHeadRow) bool {
	current, err := goldenRepositoryRevisionFromEnvelope(
		head.RevisionID,
		head.RevisionNumber,
		goldenRepositoryUUIDPointer(head.PreviousRevisionID),
		head.Payload,
		head.PayloadDigest,
		head.CreatedAt.Time,
	)
	return err == nil && head.AggregateKind == goldenRepositoryAggregateKind && revision.validate() == nil &&
		head.ScopeID != uuid.Nil && current.ID == head.RevisionID &&
		revision.Number == current.Number+1 && revision.PreviousID != nil && *revision.PreviousID == current.ID
}

func goldenRepositoryReplayMatches(replay GoldenRepositoryCommit, commit GoldenRepositoryCommit) bool {
	return replay.Command != nil && commit.Command != nil && replay.Command.ID == commit.Command.ID &&
		replay.Command.Kind == commit.Command.Kind && replay.Command.Digest == commit.Command.Digest &&
		goldenRepositoryScopesEqual(replay.Scope, commit.Scope) && goldenRepositoryRevisionEqual(replay.Revision, commit.Revision)
}

func replayRevision(replay GoldenRepositoryCommit) *GoldenRepositoryRevision {
	return cloneGoldenRepositoryRevision(replay.Revision)
}

func cloneGoldenRepositoryRevision(revision GoldenRepositoryRevision) *GoldenRepositoryRevision {
	canonical, err := goldenRepositoryCanonicalSnapshot(revision.Snapshot)
	if err != nil {
		return nil
	}
	clone := revision
	clone.PreviousID = cloneUUIDPointer(revision.PreviousID)
	clone.Snapshot = canonical
	return &clone
}

func goldenRepositoryRevisionEqual(first, second GoldenRepositoryRevision) bool {
	firstSnapshot, firstErr := goldenRepositoryCanonicalSnapshot(first.Snapshot)
	secondSnapshot, secondErr := goldenRepositoryCanonicalSnapshot(second.Snapshot)
	return firstErr == nil && secondErr == nil && first.ID == second.ID && first.Number == second.Number &&
		uuidPointersEqual(first.PreviousID, second.PreviousID) && reflect.DeepEqual(firstSnapshot, secondSnapshot) &&
		first.Digest == second.Digest && first.CreatedAt.Equal(second.CreatedAt)
}

func goldenRepositoryCommandMatches(
	journal sqlc.GoldenRepositoryCommandJournal,
	scopeID, tournamentID, revisionID uuid.UUID,
	command GoldenRepositoryCommand,
) bool {
	return journal.ScopeID == scopeID && journal.TournamentID == tournamentID && journal.CommandID == command.ID &&
		journal.CommandKind == string(command.Kind) && bytes.Equal(journal.CommandDigest, command.Digest[:]) &&
		journal.ResultRevisionID == revisionID && journal.OccurredAt.Time.Equal(command.OccurredAt)
}

func goldenRepositoryHeadMatches(row sqlc.LoadGoldenRepositoryHeadRow, revision GoldenRepositoryRevision) bool {
	stored, err := goldenRepositoryRevisionFromEnvelope(
		row.RevisionID, row.RevisionNumber, goldenRepositoryUUIDPointer(row.PreviousRevisionID),
		row.Payload, row.PayloadDigest, row.CreatedAt.Time,
	)
	return err == nil && goldenRepositoryRevisionEqual(stored, revision)
}

func (kind GoldenRepositoryStateCommandKind) valid() bool {
	return kind == GoldenRepositoryStateReady || kind == GoldenRepositoryStateNoShow ||
		kind == GoldenRepositoryStateAllocation
}

func isZeroGoldenRepositoryDigest(digest [sha256.Size]byte) bool {
	return digest == [sha256.Size]byte{}
}

func goldenRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func goldenRepositoryTimestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func goldenRepositoryNullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func goldenRepositoryUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := value.UUID
	return &result
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGoldenRepositoryDerivedRevisionID(value *domain.DerivedRevisionID) *domain.DerivedRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGoldenRepositoryDuration(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func uuidPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}
