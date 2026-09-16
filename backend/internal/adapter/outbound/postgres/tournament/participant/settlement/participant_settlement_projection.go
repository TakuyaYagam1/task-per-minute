package settlement

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	projectionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// participantSettlementLedgerEntry is the domain-shaped read model shared by
// materialization and its regression tests. It contains only normalized ledger
// authority returned by the locked projection query.
type participantSettlementLedgerEntry struct {
	RoundID                uuid.UUID
	RoundRevisionID        uuid.UUID
	RoundNumber            int16
	SourceKind             string
	SourceSeriesID         uuid.NullUUID
	SeriesResultRevisionID uuid.NullUUID
	ByeRevisionID          uuid.NullUUID
	ResultLabel            *string
	ParticipantID          uuid.UUID
	OpponentID             uuid.NullUUID
	Points                 int16
	EffectiveTimeNs        int64
	AcceptedSolveTimeNs    *int64
	StableSeed             int32
}

// SettlementLedgerEntry is the normalized ledger input accepted by the
// narrow root compatibility bridge used by terminal Swiss materialization.
type SettlementLedgerEntry = participantSettlementLedgerEntry

// ParticipantSettlementStandingsDependencies preserves the deterministic
// dependency graph for callers that still own terminal projection code.
func ParticipantSettlementStandingsDependencies(
	commandID uuid.UUID,
	ledger []SettlementLedgerEntry,
) ([]projectionpostgres.ProjectionDependencyInput, error) {
	return participantSettlementStandingsDependencies(commandID, ledger)
}

func participantSettlementProjectionLedger(
	rows []sqlc.ListParticipantSettlementSwissProjectionLedgerRow,
) []participantSettlementLedgerEntry {
	ledger := make([]participantSettlementLedgerEntry, len(rows))
	for index, row := range rows {
		ledger[index] = participantSettlementLedgerEntry{
			RoundID: row.RoundID, RoundRevisionID: row.RoundRevisionID, RoundNumber: row.RoundNumber,
			SourceKind: row.SourceKind, SourceSeriesID: row.SourceSeriesID,
			SeriesResultRevisionID: row.SeriesResultRevisionID, ByeRevisionID: row.ByeRevisionID,
			ResultLabel: row.ResultLabel, ParticipantID: row.ParticipantID, OpponentID: row.OpponentID,
			Points: row.Points, EffectiveTimeNs: row.EffectiveTimeNs,
			AcceptedSolveTimeNs: row.AcceptedSolveTimeNs, StableSeed: row.StableSeed,
		}
	}
	return ledger
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *ParticipantSettlementRepository) publishParticipantSettlementProjection(
	ctx context.Context,
	rosterID uuid.UUID,
	record gameusecase.SettlementRecord,
	result *resultpostgres.ResultCommitRecord,
) error {
	if result == nil {
		return domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	if _, err := querier.LockProjectionRoster(ctx, sqlc.LockProjectionRosterParams{
		TournamentID: record.Scope.Game.TournamentID,
		RosterID:     rosterID,
	}); err != nil {
		return participantSettlementProjectionError("lock roster", err)
	}
	if _, err := querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: record.Scope.Game.TournamentID,
		RosterID:     rosterID,
	}); err != nil {
		return participantSettlementProjectionError("lock revision set", err)
	}
	current, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: record.Scope.Game.TournamentID,
		RosterID:     rosterID,
	})
	if err != nil {
		return participantSettlementProjectionError("current revision", err)
	}
	if current.RevisionNumber != record.Evidence.SourceProjectionRevision ||
		current.RevisionNumber+1 != record.Evidence.ProjectionRevision {
		return domain.ErrConflict
	}
	previousArtifacts, err := querier.ListProjectionRevisionArtifacts(ctx, sqlc.ListProjectionRevisionArtifactsParams{
		RevisionID: current.ID, TournamentID: record.Scope.Game.TournamentID, RosterID: rosterID,
	})
	if err != nil {
		return participantSettlementProjectionError("current artifacts", err)
	}
	if len(previousArtifacts) == 0 {
		return domain.ErrConflict
	}
	materialized, dependencies, included, err := r.participantSettlementMaterialization(ctx, rosterID, record, result)
	if err != nil {
		return err
	}
	cutoffID := participantSettlementID(record.CommandID, "projection-cutoff")
	if _, err = querier.CreateProjectionCutoff(ctx, sqlc.CreateProjectionCutoffParams{
		ID: cutoffID, TournamentID: record.Scope.Game.TournamentID, RosterID: rosterID,
		SequenceNumber: current.RevisionNumber + 1, PreviousCutoffID: nullableUUIDValue(current.CutoffID),
		SourceKind:               "official_result",
		OfficialResultRevisionID: nullableUUIDValue(record.SettlementGameResultRevision.ID.UUID()),
		Reason:                   participantSettlementProjectionReason,
		CutoffAt:                 tstz(record.SettledAt), CreatedAt: tstz(record.SettledAt),
	}); err != nil {
		return mapRepositoryWriteError("ParticipantSettlementRepository - create projection cutoff", err)
	}
	if _, err = querier.CreateProjectionRevision(ctx, sqlc.CreateProjectionRevisionParams{
		ID:           record.Evidence.ProjectionRevisionID,
		TournamentID: record.Scope.Game.TournamentID, RosterID: rosterID,
		RevisionNumber: current.RevisionNumber + 1, PreviousRevisionID: nullableUUIDValue(current.ID),
		CutoffID: cutoffID, CreatedAt: tstz(record.SettledAt),
	}); err != nil {
		return mapRepositoryWriteError("ParticipantSettlementRepository - create projection revision", err)
	}
	if included {
		if err = r.persistParticipantSettlementStandings(
			ctx,
			rosterID,
			record,
			materialized,
			dependencies,
		); err != nil {
			return err
		}
	}
	for _, artifact := range previousArtifacts {
		if included && artifact.ArtifactKind == string(domain.ArtifactKindStandings) {
			continue
		}
		if _, err = querier.LinkProjectionArtifact(ctx, sqlc.LinkProjectionArtifactParams{
			RevisionID:   record.Evidence.ProjectionRevisionID,
			TournamentID: record.Scope.Game.TournamentID, RosterID: rosterID,
			ArtifactKind: artifact.ArtifactKind, ArtifactID: artifact.ArtifactID,
			ChangeKind: "reused", CreatedAt: tstz(record.SettledAt),
		}); err != nil {
			return mapRepositoryWriteError("ParticipantSettlementRepository - reuse projection artifact", err)
		}
	}
	if result.Outbox.ID != result.Commit.OutboxEventID ||
		result.Outbox.ProjectionRevisionID != record.Evidence.ProjectionRevisionID ||
		result.Outbox.ProjectionRevision != record.Evidence.ProjectionRevision ||
		result.Outbox.ProjectionOrdinal < 1 {
		return domain.ErrConflict
	}
	if _, err = querier.SupersedeProjectionRevisionCAS(ctx, sqlc.SupersedeProjectionRevisionCASParams{
		SupersededByRevisionID: nullableUUIDValue(record.Evidence.ProjectionRevisionID),
		SupersededAt:           tstz(record.SettledAt),
		SupersessionReason:     optionalTrimmedString(participantSettlementProjectionReason),
		ID:                     current.ID,
		TournamentID:           record.Scope.Game.TournamentID,
		RosterID:               rosterID,
	}); err != nil {
		return participantSettlementProjectionError("supersede revision", err)
	}
	return resultpostgres.PublishCommittedResultProjection(
		ctx,
		r.tx,
		record.Scope.Game.TournamentID,
		rosterID,
		record.Evidence.ProjectionRevisionID,
		record.SettledAt,
		r.finalizer,
	)
}

func (r *ParticipantSettlementRepository) participantSettlementMaterialization(
	ctx context.Context,
	rosterID uuid.UUID,
	record gameusecase.SettlementRecord,
	result *resultpostgres.ResultCommitRecord,
) (projection.CanonicalMaterialization, []projectionpostgres.ProjectionDependencyInput, bool, error) {
	if record.Series.CurrentResultRevisionID == nil || result == nil ||
		!result.Commit.SeriesResultRevisionID.Valid {
		return projection.CanonicalMaterialization{}, nil, false, nil
	}
	querier := r.tx.Querier(ctx)
	participants, err := querier.ListTournamentAdminCorrectionProjectionParticipants(ctx, rosterID)
	if err != nil {
		return projection.CanonicalMaterialization{}, nil, false,
			participantSettlementProjectionError("projection participants", err)
	}
	ledger, err := querier.ListParticipantSettlementSwissProjectionLedger(
		ctx,
		sqlc.ListParticipantSettlementSwissProjectionLedgerParams{
			TournamentID: record.Scope.Game.TournamentID,
			RosterID:     rosterID,
			WaveID:       record.Scope.WaveID,
		},
	)
	if err != nil {
		return projection.CanonicalMaterialization{}, nil, false,
			participantSettlementProjectionError("Swiss point ledger", err)
	}
	canonicalLedger := participantSettlementProjectionLedger(ledger)
	if !participantSettlementLedgerIncludes(
		canonicalLedger,
		result.Commit.SeriesResultRevisionID.UUID,
	) {
		return projection.CanonicalMaterialization{}, nil, false, nil
	}
	input, err := participantSettlementMaterializationInput(
		record.Scope.Game.TournamentID,
		participants,
		canonicalLedger,
	)
	if err != nil {
		return projection.CanonicalMaterialization{}, nil, false, err
	}
	materialized, err := projection.BuildCanonicalMaterialization(input)
	if err != nil {
		return projection.CanonicalMaterialization{}, nil, false,
			fmt.Errorf("ParticipantSettlementRepository - materialize canonical projection: %w", err)
	}
	dependencies, err := participantSettlementStandingsDependencies(record.CommandID, canonicalLedger)
	if err != nil {
		return projection.CanonicalMaterialization{}, nil, false, err
	}
	return materialized, dependencies, true, nil
}

func participantSettlementLedgerIncludes(
	ledger []participantSettlementLedgerEntry,
	seriesResultRevisionID uuid.UUID,
) bool {
	if seriesResultRevisionID == uuid.Nil {
		return false
	}
	for _, entry := range ledger {
		if entry.SourceKind == "series" && entry.SeriesResultRevisionID.Valid &&
			entry.SeriesResultRevisionID.UUID == seriesResultRevisionID {
			return true
		}
	}
	return false
}

func participantSettlementMaterializationInput(
	tournamentID uuid.UUID,
	participants []sqlc.ListTournamentAdminCorrectionProjectionParticipantsRow,
	ledger []participantSettlementLedgerEntry,
) (projection.CanonicalMaterializationInput, error) {
	canonicalParticipants := make([]projection.CanonicalSwissParticipant, len(participants))
	for index, participant := range participants {
		if participant.ID == uuid.Nil || participant.Seed < 1 {
			return projection.CanonicalMaterializationInput{}, domain.ErrInternal
		}
		canonicalParticipants[index] = projection.CanonicalSwissParticipant{
			ID: participant.ID, StableSeed: int(participant.Seed),
		}
	}
	canonicalLedger := make([]projection.CanonicalSwissPointLedgerEntry, len(ledger))
	for index, entry := range ledger {
		canonical, err := participantSettlementCanonicalLedgerEntry(entry)
		if err != nil {
			return projection.CanonicalMaterializationInput{}, err
		}
		canonicalLedger[index] = canonical
	}
	return projection.CanonicalMaterializationInput{
		TournamentID: tournamentID, Participants: canonicalParticipants, SwissLedger: canonicalLedger,
		ArtifactKinds: []domain.ArtifactKind{domain.ArtifactKindStandings},
	}, nil
}

func participantSettlementCanonicalLedgerEntry(
	entry participantSettlementLedgerEntry,
) (projection.CanonicalSwissPointLedgerEntry, error) {
	if entry.RoundRevisionID == uuid.Nil || entry.RoundID == uuid.Nil || entry.ParticipantID == uuid.Nil ||
		entry.RoundNumber < 1 || entry.StableSeed < 1 || entry.Points < 0 || entry.EffectiveTimeNs < 0 {
		return projection.CanonicalSwissPointLedgerEntry{}, domain.ErrInternal
	}
	canonical := projection.CanonicalSwissPointLedgerEntry{
		RoundID: entry.RoundID, RoundRevisionID: entry.RoundRevisionID, RoundNumber: int(entry.RoundNumber),
		SourceKind: swissusecase.PointSourceKind(entry.SourceKind), ResultLabel: swissusecase.SeriesResultLabel(stringValue(entry.ResultLabel)),
		ParticipantID: entry.ParticipantID, Points: int(entry.Points),
		EffectiveTime: time.Duration(entry.EffectiveTimeNs), StableSeed: int(entry.StableSeed),
	}
	if entry.SourceSeriesID.Valid {
		canonical.SourceSeriesID = entry.SourceSeriesID.UUID
	}
	if entry.SeriesResultRevisionID.Valid {
		canonical.SeriesResultRevisionID = entry.SeriesResultRevisionID.UUID
	}
	if entry.ByeRevisionID.Valid {
		canonical.ByeRevisionID = entry.ByeRevisionID.UUID
	}
	if entry.OpponentID.Valid {
		opponentID := entry.OpponentID.UUID
		canonical.OpponentID = &opponentID
	}
	if entry.AcceptedSolveTimeNs != nil {
		accepted := time.Duration(*entry.AcceptedSolveTimeNs)
		canonical.AcceptedSolveTime = &accepted
	}
	return canonical, nil
}

func participantSettlementStandingsDependencies(
	commandID uuid.UUID,
	ledger []participantSettlementLedgerEntry,
) ([]projectionpostgres.ProjectionDependencyInput, error) {
	type seriesResultReference struct {
		seriesID   uuid.UUID
		revisionID uuid.UUID
	}
	seen := make(map[seriesResultReference]struct{}, len(ledger))
	for _, entry := range ledger {
		if entry.SourceKind != "series" {
			continue
		}
		if !entry.SourceSeriesID.Valid || !entry.SeriesResultRevisionID.Valid ||
			entry.SourceSeriesID.UUID == uuid.Nil || entry.SeriesResultRevisionID.UUID == uuid.Nil {
			return nil, domain.ErrInternal
		}
		seen[seriesResultReference{
			seriesID: entry.SourceSeriesID.UUID, revisionID: entry.SeriesResultRevisionID.UUID,
		}] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, domain.ErrInternal
	}
	references := make([]seriesResultReference, 0, len(seen))
	for reference := range seen {
		references = append(references, reference)
	}
	sort.Slice(references, func(first, second int) bool {
		if references[first].seriesID != references[second].seriesID {
			return references[first].seriesID.String() < references[second].seriesID.String()
		}
		return references[first].revisionID.String() < references[second].revisionID.String()
	})
	dependencies := make([]projectionpostgres.ProjectionDependencyInput, len(references))
	for index, reference := range references {
		seriesID := reference.seriesID
		revisionID := reference.revisionID
		dependencies[index] = projectionpostgres.ProjectionDependencyInput{
			ID: participantSettlementID(
				commandID,
				"projection-dependency:standings:series:"+seriesID.String()+":"+revisionID.String(),
			),
			Kind:                     "official_result",
			OfficialResultRevisionID: &revisionID,
			OfficialResultSeriesID:   &seriesID,
		}
	}
	return dependencies, nil
}

func (r *ParticipantSettlementRepository) persistParticipantSettlementStandings(
	ctx context.Context,
	rosterID uuid.UUID,
	record gameusecase.SettlementRecord,
	materialized projection.CanonicalMaterialization,
	dependencies []projectionpostgres.ProjectionDependencyInput,
) error {
	if len(materialized.Artifacts) != 1 || materialized.Artifacts[0].Kind != domain.ArtifactKindStandings ||
		len(dependencies) == 0 {
		return domain.ErrInternal
	}
	artifact := materialized.Artifacts[0]
	members := make([]projectionpostgres.ProjectionMemberInput, len(artifact.Members))
	for index, member := range artifact.Members {
		members[index] = projectionpostgres.ProjectionMemberInput{
			//nolint:gosec // Domain validation bounds this value before the storage conversion.
			ParticipantID: member.ParticipantID, Position: int32(member.Position), ScoreMilli: member.ScoreMilli,
		}
	}
	input := projectionpostgres.ProjectionPublishInput{
		IDs:       projectionpostgres.ProjectionIDs{RevisionID: record.Evidence.ProjectionRevisionID},
		Scope:     projectionpostgres.ProjectionScope{TournamentID: record.Scope.Game.TournamentID, RosterID: rosterID},
		CreatedAt: record.SettledAt,
	}
	return projectionpostgres.CreateProjectionArtifact(ctx, r.tx.Querier(ctx), input, projectionpostgres.ProjectionArtifactInput{
		ID:   participantSettlementID(record.CommandID, "projection-artifact:standings"),
		Kind: domain.ArtifactKindStandings, Key: "standings", Payload: artifact.Payload,
		PayloadDigest: artifact.PayloadDigest, Members: members, Dependencies: dependencies,
	})
}

func participantSettlementProjectionError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return fmt.Errorf("ParticipantSettlementRepository - projection %s: %w", operation, err)
}
