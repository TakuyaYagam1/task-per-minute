package result

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	projectionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projectionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// persistTerminalProjectionNodes normalizes terminal no-show and forfeit
// evidence before the shared result projection is published. It is local to
// this package so result persistence does not depend on the parent adapter.
//
//nolint:gocyclo // Locking and fail-closed validation intentionally stay ordered.
func persistTerminalProjectionNodes(ctx context.Context, tx *db.TxManager, in ResultSettlementInput) error {
	q := tx.Querier(ctx)
	commits, err := q.LockTerminalProjectionCommit(ctx, sqlc.LockTerminalProjectionCommitParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID, ProjectionRevisionID: in.IDs.ProjectionEvidenceID,
	})
	if err != nil || len(commits) == 0 {
		return err
	}
	if len(commits) != 1 || !commits[0].ResolvedAt.Valid || !commits[0].ResolvedAt.Time.Equal(in.SettledAt) {
		return domain.ErrConflict
	}
	commit := commits[0]
	// Live forfeits already own ordinary result-commit nodes. Only their Swiss
	// point evidence needs normalization before the shared publication.
	if commit.SourceKind == "result_commit" {
		return persistTerminalSwissPoints(ctx, q, in, commit)
	}
	authorityID := uuid.NewSHA1(commit.ID, []byte("terminal-projection-authority"))
	authority := sqlc.CreateTerminalProjectionAuthorityParams{
		ID: authorityID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SourceKind: commit.SourceKind, CreatedAt: tstz(in.SettledAt),
	}
	switch commit.SourceKind {
	case "normal_no_show_commit":
		authority.NormalNoShowCommitID = nullableUUIDValue(commit.ID)
	case "operator_forfeit_commit":
		authority.OperatorForfeitCommitID = nullableUUIDValue(commit.ID)
	default:
		return domain.ErrConflict
	}
	if err := q.CreateTerminalProjectionAuthority(ctx, authority); err != nil {
		return err
	}
	revisions, err := q.LockTerminalProjectionRevisions(ctx, sqlc.LockTerminalProjectionRevisionsParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: commit.ResultEventID,
	})
	if err != nil {
		return err
	}
	scoreFound, resultFound := false, false
	var games []uuid.UUID
	for _, revision := range revisions {
		if !revision.CreatedAt.Valid || !revision.CreatedAt.Time.Equal(in.SettledAt) {
			return domain.ErrConflict
		}
		previous := revision.PreviousRevisionID
		if previous.Valid && revision.ArtifactKind == "series_score" && revision.RevisionNumber == 2 {
			nodes, err := q.LockStageScoreGenesisNodes(ctx, sqlc.LockStageScoreGenesisNodesParams{
				ScoreRevisionID: previous.UUID, TournamentID: in.Scope.TournamentID,
				RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID,
			})
			if err != nil {
				return err
			}
			if len(nodes) > 0 {
				id, err := stageScoreGenesisNode(in.Scope, previous.UUID, nodes)
				if err != nil {
					return err
				}
				previous = nullableUUIDValue(id)
			}
		}
		payload, err := marshalJSON("terminal projection node", map[string]any{
			"schema": "terminal-result-node-v1", "revision_id": revision.ID.String(),
			"entity_id": revision.EntityID.String(), "result_event_id": commit.ResultEventID.String(),
			"artifact_kind": revision.ArtifactKind,
		})
		if err != nil {
			return err
		}
		if err := createResultProjectionNode(ctx, q, sqlc.CreateResultProjectionNodeParams{
			ID: revision.ID, AuthorityID: authorityID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, ArtifactKind: revision.ArtifactKind,
			EntityID: revision.EntityID, RevisionNumber: revision.RevisionNumber,
			PreviousNodeID: previous, Payload: payload, PayloadDigest: digestBytes(payload),
			CreatedAt: revision.CreatedAt,
		}); err != nil {
			return err
		}
		switch revision.ArtifactKind {
		case "series_score":
			scoreFound = revision.ID == commit.SeriesScoreRevisionID
		case "series_result":
			resultFound = revision.ID == commit.SeriesResultRevisionID
		case "game_result":
			games = append(games, revision.ID)
		}
	}
	if !scoreFound || !resultFound ||
		(commit.SourceKind == "operator_forfeit_commit" && len(games) != 0) ||
		(commit.SourceKind == "normal_no_show_commit" && len(games) == 0) {
		return domain.ErrConflict
	}
	for _, gameID := range games {
		if err := createResultProjectionDependency(ctx, q, authorityID, gameID, commit.SeriesScoreRevisionID, in.SettledAt); err != nil {
			return err
		}
	}
	if err := createResultProjectionDependency(ctx, q, authorityID, commit.SeriesScoreRevisionID, commit.SeriesResultRevisionID, in.SettledAt); err != nil {
		return err
	}
	return persistTerminalSwissPoints(ctx, q, in, commit)
}

func persistTerminalSwissPoints(
	ctx context.Context,
	q *sqlc.Queries,
	in ResultSettlementInput,
	commit sqlc.LockTerminalProjectionCommitRow,
) error {
	rows, err := q.LockTerminalSwissPointSource(ctx, sqlc.LockTerminalSwissPointSourceParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultRevisionID: nullableUUIDValue(commit.SeriesResultRevisionID),
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	if len(rows) != 1 {
		return domain.ErrConflict
	}
	row := rows[0]
	participants, err := q.LockTournamentProgressionParticipants(ctx, in.Scope.RosterID)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, len(participants))
	seeds := make(map[uuid.UUID]int32, len(participants))
	for index, participant := range participants {
		ids[index], seeds[participant.ID] = participant.ID, participant.Seed
	}
	label := swissusecase.SeriesResultPlayed
	if commit.SourceKind == "normal_no_show_commit" {
		label = swissusecase.SeriesResultNoShow
		if !row.WinnerID.Valid {
			label = swissusecase.SeriesResultVoid
		}
	}
	ledger, err := swissusecase.BuildPointLedger(swissusecase.PointLedgerInput{
		ParticipantIDs: ids,
		Series: []swissusecase.SeriesPointResult{{
			RoundID: row.RoundID, RoundNumber: int(row.RoundNumber), SeriesID: in.Scope.SeriesID,
			ResultRevisionID:   domain.OfficialResultRevisionID(commit.SeriesResultRevisionID),
			FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
			WinnerID: progressionUUIDPointer(row.WinnerID), Label: label,
		}},
	})
	if err != nil {
		return err
	}
	if len(ledger.Entries) != 1 || len(ledger.Entries[0].Awards) != 2 {
		return domain.ErrConflict
	}
	for _, award := range ledger.Entries[0].Awards {
		if _, err := q.CreateSwissPointLedgerEntry(ctx, sqlc.CreateSwissPointLedgerEntryParams{
			ID:           uuid.NewSHA1(commit.CommandID, []byte("terminal-swiss-point:"+award.ParticipantID.String())),
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			RoundID: row.RoundID, RoundNumber: row.RoundNumber, SourceKind: "series",
			SourceSeriesID:         nullableUUIDValue(in.Scope.SeriesID),
			SeriesResultRevisionID: nullableUUIDValue(commit.SeriesResultRevisionID),
			ResultLabel:            optionalTrimmedString(string(label)),

			ParticipantID: award.ParticipantID, OpponentID: nullableUUIDValue(award.OpponentID),
			Points:     int16(award.Points), //nolint:gosec // Point-ledger validation bounds awards to the PostgreSQL int2 range.
			StableSeed: seeds[award.ParticipantID], CreatedAt: tstz(in.SettledAt),
		}); err != nil {
			return err
		}
	}
	return nil
}

func progressionUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	result := value.UUID
	return &result
}

// materializeTerminalSwissStandings creates the canonical standings artifact
// required by a terminal Swiss publication. Its source rows are locked in the
// same transaction as the result evidence and projection revision.
//
//nolint:gocyclo // Canonical input assembly deliberately validates every source boundary.
func materializeTerminalSwissStandings(ctx context.Context, tx *db.TxManager, in ResultSettlementInput) (bool, error) {
	q := tx.Querier(ctx)
	commits, err := q.LockTerminalProjectionCommit(ctx, sqlc.LockTerminalProjectionCommitParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ProjectionRevisionID: in.IDs.ProjectionEvidenceID,
	})
	if err != nil || len(commits) == 0 {
		return false, err
	}
	rounds, err := q.LockTournamentProgressionSwissRounds(ctx, sqlc.LockTournamentProgressionSwissRoundsParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil || len(rounds) == 0 {
		return false, err
	}
	series, err := q.LockTournamentProgressionSwissSeries(ctx, sqlc.LockTournamentProgressionSwissSeriesParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return false, err
	}
	for _, item := range series {
		if !domain.SeriesState(item.State).IsTerminal() {
			return false, nil
		}
	}
	proofs, err := q.LockTournamentProgressionSwissRoundLockProofs(ctx, sqlc.LockTournamentProgressionSwissRoundLockProofsParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return false, err
	}
	participants, err := q.LockTournamentProgressionParticipants(ctx, in.Scope.RosterID)
	if err != nil {
		return false, err
	}
	ledger, err := q.LockTournamentProgressionSwissLedger(ctx, sqlc.LockTournamentProgressionSwissLedgerParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return false, err
	}
	input, err := terminalCanonicalSwissInput(in.Scope.TournamentID, terminalRoundRevisionIDs(rounds, proofs), participants, ledger)
	if err != nil {
		return false, err
	}
	input.ArtifactKinds = []domain.ArtifactKind{domain.ArtifactKindStandings}
	materialized, err := projectionusecase.BuildCanonicalMaterialization(input)
	if err != nil {
		return false, err
	}
	if len(materialized.Artifacts) != 1 {
		return false, domain.ErrConflict
	}
	artifact := materialized.Artifacts[0]
	members := make([]projectionpostgres.ProjectionMemberInput, len(artifact.Members))
	for index, member := range artifact.Members {
		members[index] = projectionpostgres.ProjectionMemberInput{
			ParticipantID: member.ParticipantID,
			Position:      int32(member.Position), //nolint:gosec // Canonical materialization bounds positions.
			ScoreMilli:    member.ScoreMilli,
		}
	}
	dependencies, err := terminalStandingsDependencies(commits[0].CommandID, ledger)
	if err != nil {
		return false, err
	}
	err = projectionpostgres.CreateProjectionArtifact(ctx, q,
		projectionpostgres.ProjectionPublishInput{
			IDs:       projectionpostgres.ProjectionIDs{RevisionID: in.IDs.ProjectionEvidenceID},
			Scope:     projectionpostgres.ProjectionScope{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID},
			CreatedAt: in.SettledAt,
		},
		projectionpostgres.ProjectionArtifactInput{
			ID:   uuid.NewSHA1(commits[0].CommandID, []byte("terminal-swiss-standings")),
			Kind: domain.ArtifactKindStandings, Key: "standings", Payload: artifact.Payload,
			PayloadDigest: artifact.PayloadDigest, Members: members, Dependencies: dependencies,
		},
	)
	return err == nil, err
}

func terminalRoundRevisionIDs(
	rounds []sqlc.LockTournamentProgressionSwissRoundsRow,
	proofs []sqlc.SwissRoundLockProof,
) map[uuid.UUID]uuid.UUID {
	proofByRound := make(map[uuid.UUID]sqlc.SwissRoundLockProof, len(proofs))
	for _, proof := range proofs {
		proofByRound[proof.RoundID] = proof
	}
	result := make(map[uuid.UUID]uuid.UUID, len(rounds))
	for _, round := range rounds {
		proof, found := proofByRound[round.ID]
		if !found || proof.SourceProjectionRevisionID == uuid.Nil {
			return nil
		}
		result[round.ID] = proof.SourceProjectionRevisionID
	}
	return result
}

func terminalCanonicalSwissInput(
	tournamentID uuid.UUID,
	roundRevisionIDs map[uuid.UUID]uuid.UUID,
	participants []sqlc.LockTournamentProgressionParticipantsRow,
	ledger []sqlc.LockTournamentProgressionSwissLedgerRow,
) (projectionusecase.CanonicalMaterializationInput, error) {
	if tournamentID == uuid.Nil || len(roundRevisionIDs) == 0 || len(participants) == 0 || len(ledger) == 0 {
		return projectionusecase.CanonicalMaterializationInput{}, domain.ErrConflict
	}
	canonicalParticipants := make([]projectionusecase.CanonicalSwissParticipant, len(participants))
	seenParticipants := make(map[uuid.UUID]struct{}, len(participants))
	for index, participant := range participants {
		if participant.ID == uuid.Nil || participant.Seed < 1 {
			return projectionusecase.CanonicalMaterializationInput{}, domain.ErrConflict
		}
		if _, duplicate := seenParticipants[participant.ID]; duplicate {
			return projectionusecase.CanonicalMaterializationInput{}, domain.ErrConflict
		}
		seenParticipants[participant.ID] = struct{}{}
		canonicalParticipants[index] = projectionusecase.CanonicalSwissParticipant{ID: participant.ID, StableSeed: int(participant.Seed)}
	}
	canonicalLedger := make([]projectionusecase.CanonicalSwissPointLedgerEntry, len(ledger))
	for index, row := range ledger {
		entry, err := terminalCanonicalSwissLedgerEntry(row, roundRevisionIDs[row.RoundID])
		if err != nil {
			return projectionusecase.CanonicalMaterializationInput{}, err
		}
		canonicalLedger[index] = entry
	}
	return projectionusecase.CanonicalMaterializationInput{
		TournamentID: tournamentID, Participants: canonicalParticipants, SwissLedger: canonicalLedger,
		SwissComplete: true, ArtifactKinds: []domain.ArtifactKind{domain.ArtifactKindStandings},
	}, nil
}

func terminalCanonicalSwissLedgerEntry(
	row sqlc.LockTournamentProgressionSwissLedgerRow,
	roundRevisionID uuid.UUID,
) (projectionusecase.CanonicalSwissPointLedgerEntry, error) {
	if row.ID == uuid.Nil || row.RoundID == uuid.Nil || row.RoundNumber < 1 || row.ParticipantID == uuid.Nil ||
		roundRevisionID == uuid.Nil || row.StableSeed < 1 || row.Points < 0 || row.EffectiveTimeNs < 0 {
		return projectionusecase.CanonicalSwissPointLedgerEntry{}, domain.ErrConflict
	}
	entry := projectionusecase.CanonicalSwissPointLedgerEntry{
		RoundID: row.RoundID, RoundRevisionID: roundRevisionID, RoundNumber: int(row.RoundNumber),
		SourceKind: swissusecase.PointSourceKind(row.SourceKind), ResultLabel: swissusecase.SeriesResultLabel(stringValue(row.ResultLabel)),
		ParticipantID: row.ParticipantID, Points: int(row.Points), EffectiveTime: time.Duration(row.EffectiveTimeNs), StableSeed: int(row.StableSeed),
	}
	if row.SourceSeriesID.Valid {
		entry.SourceSeriesID = row.SourceSeriesID.UUID
	}
	if row.SeriesResultRevisionID.Valid {
		entry.SeriesResultRevisionID = row.SeriesResultRevisionID.UUID
	}
	if row.ByeRevisionID.Valid {
		entry.ByeRevisionID = row.ByeRevisionID.UUID
	}
	if row.OpponentID.Valid {
		value := row.OpponentID.UUID
		entry.OpponentID = &value
	}
	if row.AcceptedSolveTimeNs != nil {
		value := time.Duration(*row.AcceptedSolveTimeNs)
		entry.AcceptedSolveTime = &value
	}
	return entry, nil
}

func terminalStandingsDependencies(
	commandID uuid.UUID,
	ledger []sqlc.LockTournamentProgressionSwissLedgerRow,
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
		seen[seriesResultReference{seriesID: entry.SourceSeriesID.UUID, revisionID: entry.SeriesResultRevisionID.UUID}] = struct{}{}
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
		seriesID, revisionID := reference.seriesID, reference.revisionID
		dependencies[index] = projectionpostgres.ProjectionDependencyInput{
			ID:   uuid.NewSHA1(commandID, []byte("participant-settlement:projection-dependency:standings:series:"+seriesID.String()+":"+revisionID.String())),
			Kind: "official_result", OfficialResultRevisionID: &revisionID, OfficialResultSeriesID: &seriesID,
		}
	}
	return dependencies, nil
}
