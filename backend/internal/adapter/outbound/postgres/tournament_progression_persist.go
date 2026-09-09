package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func (r *TournamentProgressionPostgres) PersistStageProgression(ctx context.Context, plan tournamentprogression.Plan, publication *tournamentprogression.PlayoffPublication) (tournamentprogression.PersistenceReceipt, error) {
	if r == nil || r.tx == nil || ctx == nil {
		return tournamentprogression.PersistenceReceipt{}, domain.ErrValidation
	}
	now, err := progressionPlanTime(plan)
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	if !progressionPublicationMatches(plan, publication) {
		return tournamentprogression.PersistenceReceipt{}, domain.ErrConflict
	}
	var receipt tournamentprogression.PersistenceReceipt
	err = r.tx.Do(ctx, func(txCtx context.Context) error {
		receipt, err = r.persistStageProgression(txCtx, plan, publication, now)
		return err
	})
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	return receipt, nil
}

func createProgressionProof(ctx context.Context, q *sqlc.Queries, params sqlc.CreateTournamentStageProgressionCASParams) error {
	id, err := q.CreateTournamentStageProgressionCAS(ctx, params)
	return progressionWrittenID("create stage proof", params.CommandID, id, err)
}

func progressionProofMatches(want sqlc.CreateTournamentStageProgressionCASParams, got sqlc.TournamentStageProgression) error {
	// pgx encodes timestamptz as microseconds. The proof JSON retains the
	// original planner timestamp; only database timestamp columns roundtrip
	// through PostgreSQL's precision.
	executedAt := want.ExecutedAt.Time.Truncate(time.Microsecond)
	if !progressionProofScopeMatches(want, got) || !progressionProofStateMatches(want, got) ||
		!want.ExecutedAt.Valid || !domain.IsValidServerTime(want.ExecutedAt.Time.UTC()) ||
		!got.ExecutedAt.Valid || !got.ExecutedAt.Time.Equal(executedAt) || !got.CreatedAt.Valid || !got.CreatedAt.Time.Equal(executedAt) ||
		!progressionBytesEqual(got.ProofDigest, want.ProofDigest) || !progressionJSONEqual(got.Proof, want.Proof) {
		return domain.ErrConflict
	}
	if want.ResultingProjectionRevision == nil {
		if got.ResultingProjectionRevision != nil {
			return domain.ErrConflict
		}
	} else if got.ResultingProjectionRevision == nil || *got.ResultingProjectionRevision != *want.ResultingProjectionRevision {
		return domain.ErrConflict
	}
	return nil
}

func progressionJSONEqual(left, right []byte) bool {
	canonical := func(raw []byte) ([]byte, error) {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if !json.Valid(raw) {
			return nil, domain.ErrConflict
		}
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	a, err := canonical(left)
	if err != nil {
		return false
	}
	b, err := canonical(right)
	return err == nil && bytes.Equal(a, b)
}

func progressionPublicationMatches(plan tournamentprogression.Plan, publication *tournamentprogression.PlayoffPublication) bool {
	if plan.Record.Command.Action == tournamentprogression.ActionStartGolden {
		return publication == nil
	}
	if publication == nil || plan.Bracket == nil || plan.Top4 == nil {
		return false
	}
	matches := plan.Bracket.Semifinals()
	return len(matches) == 2 && publication.PublishedProjectionID == plan.PublicationIDs.ProjectionRevisionID &&
		publication.PublishedRevision == plan.Record.Source.Revision+1 && publication.Top4ArtifactID == plan.PublicationIDs.Top4ArtifactID &&
		publication.BracketArtifactID == plan.PublicationIDs.BracketArtifactID && publication.SemifinalSeriesIDs == [2]uuid.UUID{matches[0].Series.ID, matches[1].Series.ID}
}

func persistProgressionGoldenGroups(ctx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan, now time.Time) error {
	for index := range plan.Record.TieGroups {
		if err := persistProgressionGoldenGroup(ctx, q, plan, now, index); err != nil {
			return err
		}
	}
	return nil
}

func persistProgressionGoldenGroup(ctx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan, now time.Time, index int) error {
	command, group := plan.Record.Command, plan.Record.TieGroups[index]
	golden := plan.GoldenGroups[index]
	if err := validateProgressionGoldenGroup(plan, index); err != nil {
		return err
	}
	positionFrom, err := progressionInt16(group.PositionFrom)
	if err != nil {
		return err
	}
	positionTo, err := progressionInt16(group.PositionTo)
	if err != nil {
		return err
	}
	id, err := q.CreateTournamentStageTieGroup(ctx, sqlc.CreateTournamentStageTieGroupParams{
		CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID, GroupID: group.GroupID, GroupRevisionID: group.RevisionID,
		SourceProjectionRevisionID: plan.Record.Source.RevisionID, SourceProjectionRevision: plan.Record.Source.Revision, PositionFrom: positionFrom, PositionTo: positionTo,
		Proof: group.Proof, ProofDigest: group.ProofDigest[:], CreatedAt: tstz(now),
	})
	if err := progressionWrittenID("create tie group", group.GroupID, id, err); err != nil {
		return err
	}
	if err := persistProgressionGoldenMembers(ctx, q, plan, now, index); err != nil {
		return err
	}
	digest := golden.Revision.PayloadDigest()
	id, err = q.CreateGoldenGroupRevision(ctx, sqlc.CreateGoldenGroupRevisionParams{RevisionID: group.RevisionID, GroupID: group.GroupID, StageProgressionCommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID,
		SourceProjectionRevisionID: plan.Record.Source.RevisionID, SourceProjectionRevision: plan.Record.Source.Revision, PositionFrom: positionFrom, PositionTo: positionTo, Definition: golden.Revision.Payload(), DefinitionDigest: digest[:], CreatedAt: tstz(now)})
	if err := progressionWrittenID("create Golden revision", group.RevisionID, id, err); err != nil {
		return err
	}
	return nil
}

func readProgressionPersistence(ctx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan, publication *tournamentprogression.PlayoffPublication, now time.Time) (tournamentprogression.PersistenceReceipt, error) {
	command := plan.Record.Command
	source, err := q.GetProjectionRevisionScoped(ctx, sqlc.GetProjectionRevisionScopedParams{ID: plan.Record.Source.RevisionID, TournamentID: command.TournamentID, RosterID: command.RosterID})
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, projectionCASWriteError("read persisted source", err)
	}
	if !progressionPersistenceSourceMatches(plan, source) {
		return tournamentprogression.PersistenceReceipt{}, domain.ErrConflict
	}
	artifact, err := loadProgressionSourceArtifact(ctx, q, plan)
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	receipt := tournamentprogression.PersistenceReceipt{CommandID: command.CommandID, SourceProjectionState: source.State}
	if publication == nil {
		if source.State != "published" || source.SupersededByRevisionID.Valid {
			return tournamentprogression.PersistenceReceipt{}, domain.ErrConflict
		}
		return receipt, nil
	}
	if source.State != "superseded" || !source.SupersededByRevisionID.Valid || source.SupersededByRevisionID.UUID != publication.PublishedProjectionID {
		return tournamentprogression.PersistenceReceipt{}, domain.ErrConflict
	}
	scope := ProjectionScope{TournamentID: command.TournamentID, RosterID: command.RosterID}
	input, err := progressionPublicationInput(plan, artifact, now)
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	record, err := loadProjectionRecord(ctx, q, scope, publication.PublishedProjectionID)
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	if err := progressionPublishedRecord(plan, input, record); err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	receipt.SourceSupersededByRevisionID = source.SupersededByRevisionID.UUID
	receipt.PublishedProjectionID, receipt.PublishedRevision = record.Revision.ID, record.Revision.RevisionNumber
	return progressionPersistenceArtifacts(receipt, record)
}

func progressionPersistenceSourceMatches(plan tournamentprogression.Plan, source sqlc.ProjectionRevision) bool {
	return source.ID == plan.Record.Source.RevisionID && source.TournamentID == plan.Record.Command.TournamentID &&
		source.RosterID == plan.Record.Command.RosterID && source.RevisionNumber == plan.Record.Source.Revision
}

func (r *TournamentProgressionPostgres) persistStageProgression(txCtx context.Context, plan tournamentprogression.Plan, publication *tournamentprogression.PlayoffPublication, now time.Time) (tournamentprogression.PersistenceReceipt, error) {
	q := r.tx.Querier(txCtx)
	command := plan.Record.Command
	if err := checkProgressionReplay(txCtx, q, plan.Record.Command, "check persistence replay"); err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	tournament, err := q.GetTournamentSummary(txCtx, command.TournamentID)
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, projectionCASWriteError("read progression tournament", err)
	}
	view, err := tournamentProgressionTournamentView(tournament)
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	if view.ID != command.TournamentID || view.RosterID != command.RosterID || view.State != plan.Record.SourceState ||
		!now.After(view.UpdatedAt) {
		return tournamentprogression.PersistenceReceipt{}, domain.ErrConflict
	}
	next, _ := progressionActionState(command.Action)
	params := sqlc.CreateTournamentStageProgressionCASParams{
		CommandID: command.CommandID, ActorID: command.ActorID, Action: string(command.Action), SourceTournamentRevision: view.Revision, SourceTournamentState: string(view.State),
		SourceProjectionRevisionID: plan.Record.Source.RevisionID, SourceProjectionRevision: plan.Record.Source.Revision,
		ResultingTournamentState: string(next), Proof: plan.Record.Proof, ProofDigest: plan.Record.ProofDigest[:], ExecutedAt: tstz(now), TournamentID: command.TournamentID, RosterID: command.RosterID,
	}
	if publication != nil {
		params.ResultingProjectionRevisionID = nullableUUIDValue(publication.PublishedProjectionID)
		params.ResultingProjectionRevision = &publication.PublishedRevision
	}
	if err := createProgressionProof(txCtx, q, params); err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	if publication == nil {
		if err := persistProgressionGoldenGroups(txCtx, q, plan, now); err != nil {
			return tournamentprogression.PersistenceReceipt{}, err
		}
	} else if err := r.persistPlayoffEvidence(txCtx, q, plan, *publication, now); err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	proof, err := q.GetTournamentStageProgressionProof(txCtx, sqlc.GetTournamentStageProgressionProofParams{TournamentID: command.TournamentID, RosterID: command.RosterID, CommandID: command.CommandID})
	if err != nil {
		return tournamentprogression.PersistenceReceipt{}, projectionCASWriteError("read stage proof", err)
	}
	if err := progressionProofMatches(params, proof); err != nil {
		return tournamentprogression.PersistenceReceipt{}, err
	}
	if publication != nil {
		outboxID := uuid.NewSHA1(command.CommandID, []byte("tournament-progression:outbox-event"))
		writtenID, writeErr := q.CreateStageProjectionOutboxEvent(txCtx, sqlc.CreateStageProjectionOutboxEventParams{
			StageCommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID,
			ProjectionRevisionID: publication.PublishedProjectionID, ProjectionRevision: publication.PublishedRevision,
			OutboxEventID: outboxID,
		})
		if err := progressionWrittenID("create stage outbox", outboxID, writtenID, writeErr); err != nil {
			return tournamentprogression.PersistenceReceipt{}, err
		}
	}
	return readProgressionPersistence(txCtx, q, plan, publication, now)
}

func checkProgressionReplay(ctx context.Context, q *sqlc.Queries, command tournamentprogression.Command, operation string) error {
	if _, err := q.FindTournamentStageProgression(ctx, sqlc.FindTournamentStageProgressionParams{TournamentID: command.TournamentID, CommandID: command.CommandID}); !errors.Is(err, pgx.ErrNoRows) {
		if err == nil {
			return domain.ErrConflict
		}
		return tournamentProgressionReadError(operation, err)
	}
	return nil
}

func progressionProofScopeMatches(want sqlc.CreateTournamentStageProgressionCASParams, got sqlc.TournamentStageProgression) bool {
	return got.CommandID == want.CommandID && got.TournamentID == want.TournamentID && got.RosterID == want.RosterID && got.ActorID == want.ActorID && got.Action == want.Action
}

func progressionProofStateMatches(want sqlc.CreateTournamentStageProgressionCASParams, got sqlc.TournamentStageProgression) bool {
	return got.SourceTournamentRevision == want.SourceTournamentRevision && got.SourceTournamentState == want.SourceTournamentState &&
		got.SourceProjectionRevisionID == want.SourceProjectionRevisionID && got.SourceProjectionRevision == want.SourceProjectionRevision &&
		got.ResultingProjectionRevisionID == want.ResultingProjectionRevisionID &&
		got.ResultingTournamentRevision == want.SourceTournamentRevision+1 && got.ResultingTournamentState == want.ResultingTournamentState
}

func persistProgressionGoldenMembers(ctx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan, now time.Time, index int) error {
	command, group := plan.Record.Command, plan.Record.TieGroups[index]
	for _, participantID := range group.Participants {
		position := 0
		for _, member := range plan.Record.Source.Members {
			if member.ParticipantID == participantID {
				position = member.Position
			}
		}
		if position < group.PositionFrom || position > group.PositionTo {
			return domain.ErrConflict
		}
		standingPosition, err := progressionInt16(position)
		if err != nil {
			return err
		}
		id, err := q.CreateTournamentStageTieGroupMember(ctx, sqlc.CreateTournamentStageTieGroupMemberParams{CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID, GroupID: group.GroupID, ParticipantID: participantID, StandingPosition: standingPosition, CreatedAt: tstz(now)})
		if err := progressionWrittenID("create tie member", participantID, id, err); err != nil {
			return err
		}
	}
	return nil
}

func validateProgressionGoldenGroup(plan tournamentprogression.Plan, index int) error {
	group, golden := plan.Record.TieGroups[index], plan.GoldenGroups[index]
	members := make([]uuid.UUID, len(golden.State.Members))
	for i, member := range golden.State.Members {
		members[i] = member.ParticipantID
	}
	if golden.Revision.Validate() != nil || golden.Projection.Validate() != nil || group.GroupID != golden.State.ID || group.RevisionID != golden.State.RevisionID.UUID() ||
		group.PositionFrom != golden.State.PositionFrom || group.PositionTo != golden.State.PositionTo || !slices.Equal(group.Participants, members) ||
		sha256.Sum256(group.Proof) != group.ProofDigest {
		return domain.ErrConflict
	}
	return nil
}

func progressionPersistenceArtifacts(receipt tournamentprogression.PersistenceReceipt, record *ProjectionRecord) (tournamentprogression.PersistenceReceipt, error) {
	for _, item := range record.Artifacts {
		digest, ok := progressionDigest(item.Artifact.PayloadDigest)
		if !ok {
			return tournamentprogression.PersistenceReceipt{}, domain.ErrConflict
		}
		artifact := tournamentprogression.PersistedArtifact{ID: item.Artifact.ID, Kind: domain.ArtifactKind(item.Artifact.ArtifactKind), Digest: digest}
		if artifact.Kind == domain.ArtifactKindTopFour {
			receipt.Top4 = artifact
		}
		if artifact.Kind == domain.ArtifactKindBracket {
			receipt.Bracket = artifact
		}
	}
	return receipt, nil
}
