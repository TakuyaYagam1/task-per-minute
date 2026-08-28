package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	arenaProjectionSourceInitial         = "initial"
	arenaProjectionSourceOfficialResult  = "official_result"
	arenaProjectionSourceGoldenPosition  = "golden_position"
	arenaProjectionSourceOperatorRebuild = "operator_rebuild"

	arenaProjectionDependencyArtifact       = "artifact"
	arenaProjectionDependencyOfficialResult = "official_result"
	arenaProjectionDependencyGoldenPosition = "golden_position"
)

var ErrArenaProjectionNotFound = errors.New("arena projection repository: projection not found")

type ArenaProjectionPostgres struct {
	tx *TxManager
}

type ArenaProjectionScope struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
}

type ArenaProjectionIDs struct {
	RevisionID uuid.UUID
	CutoffID   uuid.UUID
}

type ArenaProjectionSource struct {
	Kind                     string
	OfficialResultRevisionID *uuid.UUID
	GoldenPositionCommitID   *uuid.UUID
	Reason                   string
}

type ArenaProjectionMemberInput struct {
	ParticipantID uuid.UUID
	Position      int32
	ScoreMilli    *int64
}

type ArenaProjectionDependencyInput struct {
	ID                       uuid.UUID
	Kind                     string
	DependsOnArtifactID      *uuid.UUID
	OfficialResultRevisionID *uuid.UUID
	OfficialResultSeriesID   *uuid.UUID
	GoldenPositionCommitID   *uuid.UUID
}

type ArenaProjectionArtifactInput struct {
	ID            uuid.UUID
	Kind          domain.ArenaArtifactKind
	Key           string
	Payload       json.RawMessage
	PayloadDigest [32]byte
	Members       []ArenaProjectionMemberInput
	Dependencies  []ArenaProjectionDependencyInput
}

type ArenaProjectionPublishInput struct {
	IDs                ArenaProjectionIDs
	Scope              ArenaProjectionScope
	Source             ArenaProjectionSource
	Artifacts          []ArenaProjectionArtifactInput
	SupersessionReason string
	CutoffAt           time.Time
	CreatedAt          time.Time
	PublishedAt        time.Time
}

type ArenaProjectionArtifactRecord struct {
	Artifact     sqlc.ArenaProjectionArtifact
	Members      []sqlc.ArenaProjectionArtifactMember
	Dependencies []sqlc.ArenaProjectionDependency
}

type ArenaProjectionRecord struct {
	Revision  sqlc.ArenaProjectionRevision
	Cutoff    sqlc.ArenaProjectionCutoff
	Artifacts []ArenaProjectionArtifactRecord
}

func NewArenaProjectionPostgres(tx *TxManager) *ArenaProjectionPostgres {
	return &ArenaProjectionPostgres{tx: tx}
}

func (r *ArenaProjectionPostgres) Publish(
	ctx context.Context,
	in ArenaProjectionPublishInput,
) (*ArenaProjectionRecord, error) {
	if r == nil || r.tx == nil || !validArenaProjectionPublishInput(in) {
		return nil, domain.ErrValidation
	}

	var record *ArenaProjectionRecord
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaProjectionRoster(txCtx, sqlc.LockArenaProjectionRosterParams{
			RosterID: in.Scope.RosterID, TournamentID: in.Scope.TournamentID,
		}); err != nil {
			return arenaProjectionLookupError("Publish - lock roster", err)
		}
		if _, err := querier.LockArenaProjectionRevisionSet(txCtx, sqlc.LockArenaProjectionRevisionSetParams{
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		}); err != nil {
			return fmt.Errorf("ArenaProjectionPostgres - Publish - lock revisions: %w", err)
		}

		current, currentErr := querier.GetCurrentArenaProjectionRevision(
			txCtx,
			sqlc.GetCurrentArenaProjectionRevisionParams{
				TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			},
		)
		if currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows) {
			return fmt.Errorf("ArenaProjectionPostgres - Publish - current revision: %w", currentErr)
		}

		sequence := int64(1)
		previousRevisionID := uuid.NullUUID{}
		previousCutoffID := uuid.NullUUID{}
		if currentErr == nil {
			sequence = current.RevisionNumber + 1
			previousRevisionID = nullableUUIDValue(current.ID)
			previousCutoffID = nullableUUIDValue(current.CutoffID)
		}
		if err := createArenaProjectionDraft(txCtx, querier, in, sequence, previousRevisionID, previousCutoffID); err != nil {
			return err
		}
		if currentErr == nil {
			if _, err := querier.SupersedeArenaProjectionRevisionCAS(
				txCtx,
				sqlc.SupersedeArenaProjectionRevisionCASParams{
					SupersededByRevisionID: nullableUUIDValue(in.IDs.RevisionID),
					SupersededAt:           tstz(in.PublishedAt),
					SupersessionReason:     optionalTrimmedString(in.SupersessionReason),
					ID:                     current.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
				},
			); err != nil {
				return arenaProjectionCASWriteError("supersede current revision", err)
			}
		}
		if _, err := querier.PublishArenaProjectionRevisionCAS(
			txCtx,
			sqlc.PublishArenaProjectionRevisionCASParams{
				PublishedAt: tstz(in.PublishedAt), ID: in.IDs.RevisionID,
				TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			},
		); err != nil {
			return arenaProjectionCASWriteError("publish revision", err)
		}
		loaded, err := loadArenaProjectionRecord(txCtx, querier, in.Scope, in.IDs.RevisionID)
		if err != nil {
			return err
		}
		record = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (r *ArenaProjectionPostgres) Current(
	ctx context.Context,
	scope ArenaProjectionScope,
) (*ArenaProjectionRecord, error) {
	if r == nil || r.tx == nil || !validArenaProjectionScope(scope) {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	revision, err := querier.GetCurrentArenaProjectionRevision(ctx, sqlc.GetCurrentArenaProjectionRevisionParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, arenaProjectionLookupError("Current", err)
	}
	return loadArenaProjectionRecord(ctx, querier, scope, revision.ID)
}

func (r *ArenaProjectionPostgres) CurrentStandings(
	ctx context.Context,
	scope ArenaProjectionScope,
) (*ArenaProjectionArtifactRecord, error) {
	if r == nil || r.tx == nil || !validArenaProjectionScope(scope) {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	artifact, err := querier.GetCurrentArenaStandingsArtifact(
		ctx,
		sqlc.GetCurrentArenaStandingsArtifactParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	)
	if err != nil {
		return nil, arenaProjectionLookupError("CurrentStandings", err)
	}
	return loadArenaProjectionArtifact(ctx, querier, scope, artifact)
}

func (r *ArenaProjectionPostgres) CurrentBracket(
	ctx context.Context,
	scope ArenaProjectionScope,
) (*ArenaProjectionArtifactRecord, error) {
	if r == nil || r.tx == nil || !validArenaProjectionScope(scope) {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	artifact, err := querier.GetCurrentArenaBracketArtifact(ctx, sqlc.GetCurrentArenaBracketArtifactParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, arenaProjectionLookupError("CurrentBracket", err)
	}
	return loadArenaProjectionArtifact(ctx, querier, scope, artifact)
}

func (r *ArenaProjectionPostgres) History(
	ctx context.Context,
	scope ArenaProjectionScope,
) ([]sqlc.ArenaProjectionRevision, error) {
	if r == nil || r.tx == nil || !validArenaProjectionScope(scope) {
		return nil, domain.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListArenaProjectionRevisions(
		ctx,
		sqlc.ListArenaProjectionRevisionsParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ArenaProjectionPostgres - History: %w", err)
	}
	return rows, nil
}

func createArenaProjectionDraft(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaProjectionPublishInput,
	sequence int64,
	previousRevisionID uuid.NullUUID,
	previousCutoffID uuid.NullUUID,
) error {
	_, err := querier.CreateArenaProjectionCutoff(ctx, sqlc.CreateArenaProjectionCutoffParams{
		ID: in.IDs.CutoffID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SequenceNumber: sequence, PreviousCutoffID: previousCutoffID, SourceKind: in.Source.Kind,
		OfficialResultRevisionID: nullableUUID(in.Source.OfficialResultRevisionID),
		GoldenPositionCommitID:   nullableUUID(in.Source.GoldenPositionCommitID),
		Reason:                   in.Source.Reason, CutoffAt: tstz(in.CutoffAt), CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaProjectionPostgres - Publish - create cutoff", err)
	}
	_, err = querier.CreateArenaProjectionRevision(ctx, sqlc.CreateArenaProjectionRevisionParams{
		ID: in.IDs.RevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		RevisionNumber: sequence, PreviousRevisionID: previousRevisionID,
		CutoffID: in.IDs.CutoffID, CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaProjectionPostgres - Publish - create revision", err)
	}
	for _, artifact := range in.Artifacts {
		if err := createArenaProjectionArtifact(ctx, querier, in, artifact); err != nil {
			return err
		}
	}
	return nil
}

func createArenaProjectionArtifact(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaProjectionPublishInput,
	artifact ArenaProjectionArtifactInput,
) error {
	kind := arenaProjectionArtifactKind(artifact.Kind)
	_, err := querier.CreateArenaProjectionArtifact(ctx, sqlc.CreateArenaProjectionArtifactParams{
		ID: artifact.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ProducedByRevisionID: in.IDs.RevisionID, ArtifactKind: kind, ArtifactKey: artifact.Key,
		Payload: append([]byte(nil), artifact.Payload...), PayloadDigest: append([]byte(nil), artifact.PayloadDigest[:]...),
		CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaProjectionPostgres - Publish - create artifact", err)
	}
	for _, member := range artifact.Members {
		_, err = querier.CreateArenaProjectionArtifactMember(ctx, sqlc.CreateArenaProjectionArtifactMemberParams{
			ArtifactID: artifact.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			ArtifactKind: kind, ParticipantID: member.ParticipantID, Position: member.Position,
			Score: arenaProjectionScore(member.ScoreMilli), CreatedAt: tstz(in.CreatedAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaProjectionPostgres - Publish - create member", err)
		}
	}
	for _, dependency := range artifact.Dependencies {
		_, err = querier.CreateArenaProjectionDependency(ctx, sqlc.CreateArenaProjectionDependencyParams{
			ID: dependency.ID, ArtifactID: artifact.ID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, DependencyKind: dependency.Kind,
			DependsOnArtifactID:      nullableUUID(dependency.DependsOnArtifactID),
			OfficialResultRevisionID: nullableUUID(dependency.OfficialResultRevisionID),
			OfficialResultSeriesID:   nullableUUID(dependency.OfficialResultSeriesID),
			GoldenPositionCommitID:   nullableUUID(dependency.GoldenPositionCommitID),
			CreatedAt:                tstz(in.CreatedAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaProjectionPostgres - Publish - create dependency", err)
		}
	}
	_, err = querier.LinkArenaProjectionArtifact(ctx, sqlc.LinkArenaProjectionArtifactParams{
		RevisionID: in.IDs.RevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ArtifactKind: kind, ArtifactID: artifact.ID, ChangeKind: "produced", CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaProjectionPostgres - Publish - link artifact", err)
	}
	return nil
}

func loadArenaProjectionRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ArenaProjectionScope,
	revisionID uuid.UUID,
) (*ArenaProjectionRecord, error) {
	revision, err := querier.GetArenaProjectionRevisionScoped(ctx, sqlc.GetArenaProjectionRevisionScopedParams{
		ID: revisionID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, arenaProjectionLookupError("load revision", err)
	}
	cutoff, err := querier.GetArenaProjectionCutoffByID(ctx, sqlc.GetArenaProjectionCutoffByIDParams{
		ID: revision.CutoffID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, arenaProjectionLookupError("load cutoff", err)
	}
	links, err := querier.ListArenaProjectionRevisionArtifacts(ctx, sqlc.ListArenaProjectionRevisionArtifactsParams{
		RevisionID: revision.ID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaProjectionPostgres - load links: %w", err)
	}
	artifacts := make([]ArenaProjectionArtifactRecord, 0, len(links))
	for _, link := range links {
		artifact, loadErr := querier.GetArenaProjectionArtifactScoped(ctx, sqlc.GetArenaProjectionArtifactScopedParams{
			ID: link.ArtifactID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		})
		if loadErr != nil {
			return nil, arenaProjectionLookupError("load artifact", loadErr)
		}
		record, loadErr := loadArenaProjectionArtifact(ctx, querier, scope, artifact)
		if loadErr != nil {
			return nil, loadErr
		}
		artifacts = append(artifacts, *record)
	}
	return &ArenaProjectionRecord{Revision: revision, Cutoff: cutoff, Artifacts: artifacts}, nil
}

func loadArenaProjectionArtifact(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ArenaProjectionScope,
	artifact sqlc.ArenaProjectionArtifact,
) (*ArenaProjectionArtifactRecord, error) {
	members, err := querier.ListArenaProjectionArtifactMembers(ctx, sqlc.ListArenaProjectionArtifactMembersParams{
		ArtifactID: artifact.ID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaProjectionPostgres - load members: %w", err)
	}
	dependencies, err := querier.ListArenaProjectionArtifactDependencies(
		ctx,
		sqlc.ListArenaProjectionArtifactDependenciesParams{
			ArtifactID: artifact.ID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ArenaProjectionPostgres - load dependencies: %w", err)
	}
	return &ArenaProjectionArtifactRecord{
		Artifact: artifact, Members: members, Dependencies: dependencies,
	}, nil
}

func validArenaProjectionPublishInput(in ArenaProjectionPublishInput) bool {
	if !validArenaProjectionScope(in.Scope) || in.IDs.RevisionID == uuid.Nil || in.IDs.CutoffID == uuid.Nil ||
		!validArenaProjectionTimes(in) || !validTrimmedText(in.Source.Reason) ||
		len(in.Artifacts) != 4 {
		return false
	}
	if !validArenaProjectionSource(in.Source) {
		return false
	}
	if !validTrimmedText(in.SupersessionReason) {
		return false
	}
	return validArenaProjectionArtifactSet(in.Artifacts)
}

func validArenaProjectionTimes(in ArenaProjectionPublishInput) bool {
	return validServerTime(in.CutoffAt) && validServerTime(in.CreatedAt) && validServerTime(in.PublishedAt) &&
		!in.CutoffAt.After(in.CreatedAt) && !in.CreatedAt.After(in.PublishedAt)
}

func validArenaProjectionArtifactSet(artifacts []ArenaProjectionArtifactInput) bool {
	wanted := map[domain.ArenaArtifactKind]bool{
		domain.ArenaArtifactKindStandings: false,
		domain.ArenaArtifactKindBracket:   false,
		domain.ArenaArtifactKindTopFour:   false,
		domain.ArenaArtifactKindChampion:  false,
	}
	for _, artifact := range artifacts {
		if _, ok := wanted[artifact.Kind]; !ok || wanted[artifact.Kind] || !validArenaProjectionArtifact(artifact) {
			return false
		}
		wanted[artifact.Kind] = true
	}
	return true
}

func validArenaProjectionSource(source ArenaProjectionSource) bool {
	result := source.OfficialResultRevisionID != nil && *source.OfficialResultRevisionID != uuid.Nil
	golden := source.GoldenPositionCommitID != nil && *source.GoldenPositionCommitID != uuid.Nil
	switch source.Kind {
	case arenaProjectionSourceInitial, arenaProjectionSourceOperatorRebuild:
		return !result && !golden
	case arenaProjectionSourceOfficialResult:
		return result && !golden
	case arenaProjectionSourceGoldenPosition:
		return !result && golden
	default:
		return false
	}
}

func validArenaProjectionArtifact(artifact ArenaProjectionArtifactInput) bool {
	if artifact.ID == uuid.Nil || !validTrimmedText(artifact.Key) ||
		len(artifact.Key) > 128 || !json.Valid(artifact.Payload) || zeroDigest(artifact.PayloadDigest[:]) ||
		len(artifact.Members) == 0 || len(artifact.Dependencies) == 0 {
		return false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		return false
	}
	if !validArenaProjectionMembers(artifact) || !validArenaProjectionPayloadShape(artifact, payload) {
		return false
	}
	for _, dependency := range artifact.Dependencies {
		if !validArenaProjectionDependency(dependency) {
			return false
		}
	}
	return true
}

func validArenaProjectionMembers(artifact ArenaProjectionArtifactInput) bool {
	members := make(map[uuid.UUID]struct{}, len(artifact.Members))
	positions := make(map[int32]struct{}, len(artifact.Members))
	for _, member := range artifact.Members {
		if member.ParticipantID == uuid.Nil || member.Position < 1 {
			return false
		}
		if _, exists := members[member.ParticipantID]; exists {
			return false
		}
		if _, exists := positions[member.Position]; exists {
			return false
		}
		members[member.ParticipantID] = struct{}{}
		positions[member.Position] = struct{}{}
		if artifact.Kind == domain.ArenaArtifactKindStandings {
			if member.ScoreMilli == nil || *member.ScoreMilli < 0 {
				return false
			}
		} else if member.ScoreMilli != nil {
			return false
		}
	}
	return true
}

func validArenaProjectionPayloadShape(
	artifact ArenaProjectionArtifactInput,
	payload map[string]json.RawMessage,
) bool {
	switch artifact.Kind {
	case domain.ArenaArtifactKindStandings:
		_, ok := payload["entries"]
		return ok
	case domain.ArenaArtifactKindBracket:
		_, ok := payload["rounds"]
		return ok
	case domain.ArenaArtifactKindTopFour:
		if len(artifact.Members) != 4 {
			return false
		}
		_, ok := payload["participants"]
		return ok
	case domain.ArenaArtifactKindChampion:
		if len(artifact.Members) != 1 {
			return false
		}
		_, ok := payload["participant_id"]
		return ok
	case domain.ArenaArtifactKindGameResult,
		domain.ArenaArtifactKindSeriesScore,
		domain.ArenaArtifactKindSeriesResult,
		domain.ArenaArtifactKindGoldenGroup:
		return false
	}
	return false
}

func validArenaProjectionDependency(dependency ArenaProjectionDependencyInput) bool {
	if dependency.ID == uuid.Nil {
		return false
	}
	shape := [4]bool{
		validOptionalUUID(dependency.DependsOnArtifactID),
		validOptionalUUID(dependency.OfficialResultRevisionID),
		validOptionalUUID(dependency.OfficialResultSeriesID),
		validOptionalUUID(dependency.GoldenPositionCommitID),
	}
	switch dependency.Kind {
	case arenaProjectionDependencyArtifact:
		return shape == [4]bool{true, false, false, false}
	case arenaProjectionDependencyOfficialResult:
		return shape == [4]bool{false, true, true, false}
	case arenaProjectionDependencyGoldenPosition:
		return shape == [4]bool{false, false, false, true}
	default:
		return false
	}
}

func validOptionalUUID(value *uuid.UUID) bool {
	return value != nil && *value != uuid.Nil
}

func validTrimmedText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func validArenaProjectionScope(scope ArenaProjectionScope) bool {
	return scope.TournamentID != uuid.Nil && scope.RosterID != uuid.Nil
}

func arenaProjectionArtifactKind(kind domain.ArenaArtifactKind) string {
	if kind == domain.ArenaArtifactKindTopFour {
		return "top4"
	}
	return string(kind)
}

func arenaProjectionScore(scoreMilli *int64) pgtype.Numeric {
	if scoreMilli == nil {
		return pgtype.Numeric{}
	}
	return pgtype.Numeric{Int: big.NewInt(*scoreMilli), Exp: -3, Valid: true}
}

func arenaProjectionLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArenaProjectionNotFound
	}
	return fmt.Errorf("ArenaProjectionPostgres - %s: %w", operation, err)
}

func arenaProjectionCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapArenaRepositoryWriteError("ArenaProjectionPostgres - Publish - "+operation, err)
}
