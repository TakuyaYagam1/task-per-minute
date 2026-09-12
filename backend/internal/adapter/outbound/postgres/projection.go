package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	projectionSourceInitial          = "initial"
	projectionSourceOfficialResult   = "official_result"
	projectionSourceGoldenPosition   = "golden_position"
	projectionSourceOperatorRebuild  = "operator_rebuild"
	projectionSourceStageProgression = "stage_progression"

	projectionDependencyArtifact       = "artifact"
	projectionDependencyOfficialResult = "official_result"
	projectionDependencyGoldenPosition = "golden_position"
)

var ErrProjectionNotFound = errors.New("projection repository: projection not found")

type ProjectionPostgres struct {
	tx *TxManager
}

type ProjectionScope struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
}

type ProjectionIDs struct {
	RevisionID uuid.UUID
	CutoffID   uuid.UUID
}

type ProjectionSource struct {
	Kind                      string
	OfficialResultRevisionID  *uuid.UUID
	GoldenPositionCommitID    *uuid.UUID
	StageProgressionCommandID *uuid.UUID
	Reason                    string
}

type ProjectionMemberInput struct {
	ParticipantID uuid.UUID
	Position      int32
	ScoreMilli    *int64
}

type ProjectionDependencyInput struct {
	ID                       uuid.UUID
	Kind                     string
	DependsOnArtifactID      *uuid.UUID
	OfficialResultRevisionID *uuid.UUID
	OfficialResultSeriesID   *uuid.UUID
	GoldenPositionCommitID   *uuid.UUID
}

type ProjectionArtifactInput struct {
	ID            uuid.UUID
	Kind          domain.ArtifactKind
	Key           string
	Payload       json.RawMessage
	PayloadDigest [32]byte
	Members       []ProjectionMemberInput
	Dependencies  []ProjectionDependencyInput
}

type ProjectionPublishInput struct {
	IDs                ProjectionIDs
	Scope              ProjectionScope
	Source             ProjectionSource
	Artifacts          []ProjectionArtifactInput
	SupersessionReason string
	CutoffAt           time.Time
	CreatedAt          time.Time
	PublishedAt        time.Time
}

type ProjectionArtifactRecord struct {
	Artifact     sqlc.ProjectionArtifact
	Members      []sqlc.ProjectionArtifactMember
	Dependencies []sqlc.ProjectionDependency
}

type ProjectionRecord struct {
	Revision  sqlc.ProjectionRevision
	Cutoff    sqlc.ProjectionCutoff
	Artifacts []ProjectionArtifactRecord
}

func NewProjectionPostgres(tx *TxManager) *ProjectionPostgres {
	return &ProjectionPostgres{tx: tx}
}

func publishInitialTournamentProjection(
	ctx context.Context,
	tx *TxManager,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (*ProjectionRecord, error) {
	input, err := initialTournamentProjectionInput(tournamentID, rosterID, createdAt)
	if err != nil {
		return nil, err
	}
	return NewProjectionPostgres(tx).Publish(ctx, input)
}

func initialTournamentProjectionInput(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (ProjectionPublishInput, error) {
	scope := ProjectionScope{TournamentID: tournamentID, RosterID: rosterID}
	if !validProjectionScope(scope) || !validServerTime(createdAt) {
		return ProjectionPublishInput{}, domain.ErrValidation
	}

	identity := initialTournamentProjectionIdentity(scope)
	input := ProjectionPublishInput{
		IDs: ProjectionIDs{
			RevisionID: uuid.NewSHA1(identity, []byte("revision:1")),
			CutoffID:   uuid.NewSHA1(identity, []byte("cutoff:1")),
		},
		Scope: scope,
		Source: ProjectionSource{
			Kind:   projectionSourceInitial,
			Reason: "tournament_created",
		},
		Artifacts: []ProjectionArtifactInput{
			initialProjectionArtifact(identity, domain.ArtifactKindStandings, "standings", "entries"),
			initialProjectionArtifact(identity, domain.ArtifactKindTopFour, "top_four", "participants"),
			initialProjectionArtifact(identity, domain.ArtifactKindBracket, "bracket", "rounds"),
		},
		SupersessionReason: "initial",
		CutoffAt:           createdAt,
		CreatedAt:          createdAt,
		PublishedAt:        createdAt,
	}
	if !validProjectionPublishInput(input) {
		return ProjectionPublishInput{}, domain.ErrValidation
	}
	return input, nil
}

func initialTournamentProjectionIdentity(scope ProjectionScope) uuid.UUID {
	return uuid.NewSHA1(
		scope.TournamentID,
		[]byte("tournament_v1:projection:initial:"+scope.RosterID.String()),
	)
}

func initialProjectionArtifact(
	identity uuid.UUID,
	kind domain.ArtifactKind,
	key string,
	collection string,
) ProjectionArtifactInput {
	payload := []byte(`{"` + collection + `":[]}`)
	return ProjectionArtifactInput{
		ID:            uuid.NewSHA1(identity, []byte("artifact:"+string(kind))),
		Kind:          kind,
		Key:           key,
		Payload:       payload,
		PayloadDigest: sha256.Sum256(payload),
	}
}

func (r *ProjectionPostgres) Publish(
	ctx context.Context,
	in ProjectionPublishInput,
) (*ProjectionRecord, error) {
	if r == nil || r.tx == nil || !validProjectionPublishInput(in) {
		return nil, domain.ErrValidation
	}

	var record *ProjectionRecord
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockProjectionRoster(txCtx, sqlc.LockProjectionRosterParams{
			RosterID: in.Scope.RosterID, TournamentID: in.Scope.TournamentID,
		}); err != nil {
			return projectionLookupError("Publish - lock roster", err)
		}
		if _, err := querier.LockProjectionRevisionSet(txCtx, sqlc.LockProjectionRevisionSetParams{
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		}); err != nil {
			return fmt.Errorf("ProjectionPostgres - Publish - lock revisions: %w", err)
		}

		current, currentErr := querier.GetCurrentProjectionRevision(
			txCtx,
			sqlc.GetCurrentProjectionRevisionParams{
				TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			},
		)
		if currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows) {
			return fmt.Errorf("ProjectionPostgres - Publish - current revision: %w", currentErr)
		}

		sequence := int64(1)
		previousRevisionID := uuid.NullUUID{}
		previousCutoffID := uuid.NullUUID{}
		if currentErr == nil {
			sequence = current.RevisionNumber + 1
			previousRevisionID = nullableUUIDValue(current.ID)
			previousCutoffID = nullableUUIDValue(current.CutoffID)
		}
		if err := createProjectionDraft(txCtx, querier, in, sequence, previousRevisionID, previousCutoffID); err != nil {
			return err
		}
		if currentErr == nil {
			if _, err := querier.SupersedeProjectionRevisionCAS(
				txCtx,
				sqlc.SupersedeProjectionRevisionCASParams{
					SupersededByRevisionID: nullableUUIDValue(in.IDs.RevisionID),
					SupersededAt:           tstz(in.PublishedAt),
					SupersessionReason:     optionalTrimmedString(in.SupersessionReason),
					ID:                     current.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
				},
			); err != nil {
				return projectionCASWriteError("supersede current revision", err)
			}
		}
		if _, err := querier.PublishProjectionRevisionCAS(
			txCtx,
			sqlc.PublishProjectionRevisionCASParams{
				PublishedAt: tstz(in.PublishedAt), ID: in.IDs.RevisionID,
				TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			},
		); err != nil {
			return projectionCASWriteError("publish revision", err)
		}
		loaded, err := loadProjectionRecord(txCtx, querier, in.Scope, in.IDs.RevisionID)
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

func createProjectionDraft(
	ctx context.Context,
	querier *sqlc.Queries,
	in ProjectionPublishInput,
	sequence int64,
	previousRevisionID uuid.NullUUID,
	previousCutoffID uuid.NullUUID,
) error {
	_, err := querier.CreateProjectionCutoff(ctx, sqlc.CreateProjectionCutoffParams{
		ID: in.IDs.CutoffID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SequenceNumber: sequence, PreviousCutoffID: previousCutoffID, SourceKind: in.Source.Kind,
		OfficialResultRevisionID:  nullableUUID(in.Source.OfficialResultRevisionID),
		GoldenPositionCommitID:    nullableUUID(in.Source.GoldenPositionCommitID),
		StageProgressionCommandID: nullableUUID(in.Source.StageProgressionCommandID),
		Reason:                    in.Source.Reason, CutoffAt: tstz(in.CutoffAt), CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ProjectionPostgres - Publish - create cutoff", err)
	}
	_, err = querier.CreateProjectionRevision(ctx, sqlc.CreateProjectionRevisionParams{
		ID: in.IDs.RevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		RevisionNumber: sequence, PreviousRevisionID: previousRevisionID,
		CutoffID: in.IDs.CutoffID, CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ProjectionPostgres - Publish - create revision", err)
	}
	for _, artifact := range in.Artifacts {
		if err := createProjectionArtifact(ctx, querier, in, artifact); err != nil {
			return err
		}
	}
	return nil
}

func createProjectionArtifact(
	ctx context.Context,
	querier *sqlc.Queries,
	in ProjectionPublishInput,
	artifact ProjectionArtifactInput,
) error {
	kind := projectionArtifactKind(artifact.Kind)
	_, err := querier.CreateProjectionArtifact(ctx, sqlc.CreateProjectionArtifactParams{
		ID: artifact.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ProducedByRevisionID: in.IDs.RevisionID, ArtifactKind: kind, ArtifactKey: artifact.Key,
		Payload: append([]byte(nil), artifact.Payload...), PayloadDigest: append([]byte(nil), artifact.PayloadDigest[:]...),
		CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ProjectionPostgres - Publish - create artifact", err)
	}
	for _, member := range artifact.Members {
		_, err = querier.CreateProjectionArtifactMember(ctx, sqlc.CreateProjectionArtifactMemberParams{
			ArtifactID: artifact.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			ArtifactKind: kind, ParticipantID: member.ParticipantID, Position: member.Position,
			Score: projectionScore(member.ScoreMilli), CreatedAt: tstz(in.CreatedAt),
		})
		if err != nil {
			return mapRepositoryWriteError("ProjectionPostgres - Publish - create member", err)
		}
	}
	for _, dependency := range artifact.Dependencies {
		_, err = querier.CreateProjectionDependency(ctx, sqlc.CreateProjectionDependencyParams{
			ID: dependency.ID, ArtifactID: artifact.ID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, DependencyKind: dependency.Kind,
			DependsOnArtifactID:      nullableUUID(dependency.DependsOnArtifactID),
			OfficialResultRevisionID: nullableUUID(dependency.OfficialResultRevisionID),
			OfficialResultSeriesID:   nullableUUID(dependency.OfficialResultSeriesID),
			GoldenPositionCommitID:   nullableUUID(dependency.GoldenPositionCommitID),
			CreatedAt:                tstz(in.CreatedAt),
		})
		if err != nil {
			return mapRepositoryWriteError("ProjectionPostgres - Publish - create dependency", err)
		}
	}
	_, err = querier.LinkProjectionArtifact(ctx, sqlc.LinkProjectionArtifactParams{
		RevisionID: in.IDs.RevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ArtifactKind: kind, ArtifactID: artifact.ID, ChangeKind: "produced", CreatedAt: tstz(in.CreatedAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ProjectionPostgres - Publish - link artifact", err)
	}
	return nil
}

func validProjectionPublishInput(in ProjectionPublishInput) bool {
	if !validProjectionScope(in.Scope) || in.IDs.RevisionID == uuid.Nil || in.IDs.CutoffID == uuid.Nil ||
		!validProjectionTimes(in) || !validTrimmedText(in.Source.Reason) ||
		len(in.Artifacts) != 3 {
		return false
	}
	if !validProjectionSource(in.Source) {
		return false
	}
	if !validTrimmedText(in.SupersessionReason) {
		return false
	}
	return validProjectionArtifactSetForSource(in.Source.Kind == projectionSourceInitial, in.Artifacts)
}

func validProjectionTimes(in ProjectionPublishInput) bool {
	return validServerTime(in.CutoffAt) && validServerTime(in.CreatedAt) && validServerTime(in.PublishedAt) &&
		!in.CutoffAt.After(in.CreatedAt) && !in.CreatedAt.After(in.PublishedAt)
}

func validProjectionArtifactSetForSource(allowEmpty bool, artifacts []ProjectionArtifactInput) bool {
	if len(artifacts) != 3 {
		return false
	}
	wanted := map[domain.ArtifactKind]bool{
		domain.ArtifactKindStandings: false,
		domain.ArtifactKindBracket:   false,
		domain.ArtifactKindTopFour:   false,
	}
	for _, artifact := range artifacts {
		if _, ok := wanted[artifact.Kind]; !ok || wanted[artifact.Kind] || !validProjectionArtifactForSource(artifact, allowEmpty) {
			return false
		}
		wanted[artifact.Kind] = true
	}
	return true
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validProjectionSource(source ProjectionSource) bool {
	result := source.OfficialResultRevisionID != nil && *source.OfficialResultRevisionID != uuid.Nil
	golden := source.GoldenPositionCommitID != nil && *source.GoldenPositionCommitID != uuid.Nil
	stage := source.StageProgressionCommandID != nil && *source.StageProgressionCommandID != uuid.Nil
	switch source.Kind {
	case projectionSourceInitial, projectionSourceOperatorRebuild:
		return !result && !golden && !stage
	case projectionSourceOfficialResult:
		return result && !golden && !stage
	case projectionSourceGoldenPosition:
		return !result && golden && !stage
	case projectionSourceStageProgression:
		return !result && !golden && stage
	default:
		return false
	}
}

func validProjectionArtifact(artifact ProjectionArtifactInput) bool {
	return validProjectionArtifactForSource(artifact, false)
}

//nolint:gocyclo // Artifact validation keeps the signed payload and its relational evidence in one fail-closed boundary.
func validProjectionArtifactForSource(artifact ProjectionArtifactInput, allowEmpty bool) bool {
	if artifact.ID == uuid.Nil || !validTrimmedText(artifact.Key) ||
		len(artifact.Key) > 128 || !json.Valid(artifact.Payload) || zeroDigest(artifact.PayloadDigest[:]) ||
		sha256.Sum256(artifact.Payload) != artifact.PayloadDigest {
		return false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		return false
	}
	if allowEmpty && validInitialEmptyProjectionArtifact(artifact, payload) {
		return true
	}
	if len(artifact.Members) == 0 || len(artifact.Dependencies) == 0 {
		return false
	}
	if !validProjectionMembers(artifact) || !validProjectionPayloadShape(artifact, payload) {
		return false
	}
	for _, dependency := range artifact.Dependencies {
		if !validProjectionDependency(dependency) {
			return false
		}
	}
	return true
}

func validInitialEmptyProjectionArtifact(
	artifact ProjectionArtifactInput,
	payload map[string]json.RawMessage,
) bool {
	if len(artifact.Members) != 0 || len(artifact.Dependencies) != 0 || len(payload) != 1 {
		return false
	}

	var collection, key string
	switch artifact.Kind {
	case domain.ArtifactKindStandings:
		collection, key = "entries", "standings"
	case domain.ArtifactKindTopFour:
		collection, key = "participants", "top_four"
	case domain.ArtifactKindBracket:
		collection, key = "rounds", "bracket"
	case domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore, domain.ArtifactKindSeriesResult,
		domain.ArtifactKindGoldenGroup, domain.ArtifactKindChampion:
		return false
	}
	if artifact.Key != key {
		return false
	}
	raw, ok := payload[collection]
	if !ok {
		return false
	}
	return string(bytes.TrimSpace(raw)) == "[]" &&
		bytes.Equal(bytes.TrimSpace(artifact.Payload), []byte(`{"`+collection+`":[]}`))
}

func validProjectionMembers(artifact ProjectionArtifactInput) bool {
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
		if artifact.Kind == domain.ArtifactKindStandings {
			if member.ScoreMilli == nil || *member.ScoreMilli < 0 {
				return false
			}
		} else if member.ScoreMilli != nil {
			return false
		}
	}
	return true
}

func validProjectionPayloadShape(
	artifact ProjectionArtifactInput,
	payload map[string]json.RawMessage,
) bool {
	switch artifact.Kind {
	case domain.ArtifactKindStandings:
		_, legacy := payload["standings"]
		return !legacy && nonEmptyProjectionArray(payload, "entries")
	case domain.ArtifactKindBracket:
		_, bracketAlias := payload["bracket"]
		_, semifinalAlias := payload["semifinals"]
		return !bracketAlias && !semifinalAlias && nonEmptyProjectionArray(payload, "rounds")
	case domain.ArtifactKindTopFour:
		if len(artifact.Members) != 4 {
			return false
		}
		_, ok := payload["participants"]
		return ok
	case domain.ArtifactKindChampion:
		if len(artifact.Members) != 1 {
			return false
		}
		_, ok := payload["participant_id"]
		return ok
	case domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
		domain.ArtifactKindSeriesResult,
		domain.ArtifactKindGoldenGroup:
		return false
	}
	return false
}

func nonEmptyProjectionArray(payload map[string]json.RawMessage, key string) bool {
	raw, ok := payload[key]
	if !ok {
		return false
	}
	var entries []json.RawMessage
	return json.Unmarshal(raw, &entries) == nil && len(entries) > 0
}

func validProjectionDependency(dependency ProjectionDependencyInput) bool {
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
	case projectionDependencyArtifact:
		return shape == [4]bool{true, false, false, false}
	case projectionDependencyOfficialResult:
		return shape == [4]bool{false, true, true, false}
	case projectionDependencyGoldenPosition:
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

func validProjectionScope(scope ProjectionScope) bool {
	return scope.TournamentID != uuid.Nil && scope.RosterID != uuid.Nil
}

func projectionArtifactKind(kind domain.ArtifactKind) string {
	return string(kind)
}

func projectionScore(scoreMilli *int64) pgtype.Numeric {
	if scoreMilli == nil {
		return pgtype.Numeric{}
	}
	return pgtype.Numeric{Int: big.NewInt(*scoreMilli), Exp: -3, Valid: true}
}

func projectionLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProjectionNotFound
	}
	return fmt.Errorf("ProjectionPostgres - %s: %w", operation, err)
}

func projectionCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapRepositoryWriteError("ProjectionPostgres - Publish - "+operation, err)
}
