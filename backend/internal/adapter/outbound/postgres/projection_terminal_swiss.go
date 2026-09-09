package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func persistTerminalSwissPoints(ctx context.Context, q *sqlc.Queries, in ResultSettlementInput, commit sqlc.LockTerminalProjectionCommitRow) error {
	rows, err := q.LockTerminalSwissPointSource(ctx, sqlc.LockTerminalSwissPointSourceParams{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID, ResultRevisionID: nullableUUIDValue(commit.SeriesResultRevisionID)})
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
	ledger, err := swissusecase.BuildPointLedger(swissusecase.PointLedgerInput{ParticipantIDs: ids, Series: []swissusecase.SeriesPointResult{{
		RoundID: row.RoundID, RoundNumber: int(row.RoundNumber), SeriesID: in.Scope.SeriesID, ResultRevisionID: domain.OfficialResultRevisionID(commit.SeriesResultRevisionID),
		FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID, WinnerID: progressionUUIDPointer(row.WinnerID), Label: label,
	}}})
	if err != nil {
		return err
	}
	if len(ledger.Entries) != 1 || len(ledger.Entries[0].Awards) != 2 {
		return domain.ErrConflict
	}
	for _, award := range ledger.Entries[0].Awards {
		if _, err := q.CreateSwissPointLedgerEntry(ctx, sqlc.CreateSwissPointLedgerEntryParams{
			ID: uuid.NewSHA1(commit.CommandID, []byte("terminal-swiss-point:"+award.ParticipantID.String())), TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			RoundID: row.RoundID, RoundNumber: row.RoundNumber, SourceKind: "series", SourceSeriesID: nullableUUIDValue(in.Scope.SeriesID),
			SeriesResultRevisionID: nullableUUIDValue(commit.SeriesResultRevisionID), ResultLabel: optionalTrimmedString(string(label)),
			//nolint:gosec // Domain validation bounds this value before the storage conversion.
			ParticipantID: award.ParticipantID, OpponentID: nullableUUIDValue(award.OpponentID), Points: int16(award.Points),
			StableSeed: seeds[award.ParticipantID], CreatedAt: tstz(in.SettledAt),
		}); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func materializeTerminalSwissStandings(ctx context.Context, tx *TxManager, in ResultSettlementInput) (bool, error) {
	q := tx.Querier(ctx)
	commits, err := q.LockTerminalProjectionCommit(ctx, sqlc.LockTerminalProjectionCommitParams{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID, ProjectionRevisionID: in.IDs.ProjectionEvidenceID})
	if err != nil || len(commits) == 0 {
		return false, err
	}
	rounds, err := q.LockTournamentProgressionSwissRounds(ctx, sqlc.LockTournamentProgressionSwissRoundsParams{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID})
	if err != nil || len(rounds) == 0 {
		return false, err
	}
	series, err := q.LockTournamentProgressionSwissSeries(ctx, sqlc.LockTournamentProgressionSwissSeriesParams{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID})
	if err != nil {
		return false, err
	}
	for _, item := range series {
		if !domain.SeriesState(item.State).IsTerminal() {
			return false, nil
		}
	}
	proofs, err := q.LockTournamentProgressionSwissRoundLockProofs(ctx, sqlc.LockTournamentProgressionSwissRoundLockProofsParams{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID})
	if err != nil {
		return false, err
	}
	participants, err := q.LockTournamentProgressionParticipants(ctx, in.Scope.RosterID)
	if err != nil {
		return false, err
	}
	ledger, err := q.LockTournamentProgressionSwissLedger(ctx, sqlc.LockTournamentProgressionSwissLedgerParams{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID})
	if err != nil {
		return false, err
	}
	input, err := progressionCanonicalSwissInput(in.Scope.TournamentID, progressionRoundRevisionIDs(rounds, proofs), participants, ledger)
	if err != nil {
		return false, err
	}
	input.ArtifactKinds = []domain.ArtifactKind{domain.ArtifactKindStandings}
	materialized, err := projection.BuildCanonicalMaterialization(input)
	if err != nil {
		return false, err
	}
	if len(materialized.Artifacts) != 1 {
		return false, domain.ErrConflict
	}
	artifact := materialized.Artifacts[0]
	members := make([]ProjectionMemberInput, len(artifact.Members))
	for index, member := range artifact.Members {
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		members[index] = ProjectionMemberInput{ParticipantID: member.ParticipantID, Position: int32(member.Position), ScoreMilli: member.ScoreMilli}
	}
	canonicalLedger := make([]participantSettlementLedgerEntry, len(ledger))
	for index, entry := range ledger {
		canonicalLedger[index] = participantSettlementLedgerEntry{SourceKind: entry.SourceKind, SourceSeriesID: entry.SourceSeriesID, SeriesResultRevisionID: entry.SeriesResultRevisionID}
	}
	dependencies, err := participantSettlementStandingsDependencies(commits[0].CommandID, canonicalLedger)
	if err != nil {
		return false, err
	}
	err = createProjectionArtifact(ctx, q, ProjectionPublishInput{IDs: ProjectionIDs{RevisionID: in.IDs.ProjectionEvidenceID}, Scope: ProjectionScope{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID}, CreatedAt: in.SettledAt},
		ProjectionArtifactInput{ID: uuid.NewSHA1(commits[0].CommandID, []byte("terminal-swiss-standings")), Kind: domain.ArtifactKindStandings, Key: "standings", Payload: artifact.Payload, PayloadDigest: artifact.PayloadDigest, Members: members, Dependencies: dependencies})
	return err == nil, err
}
