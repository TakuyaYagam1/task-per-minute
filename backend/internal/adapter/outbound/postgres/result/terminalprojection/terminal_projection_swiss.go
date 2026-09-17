package terminalprojection

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	settlementrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/settlement"
	progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projectionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// PersistTerminalSwissPoints writes the normalized Swiss point evidence for a
// settled result. The caller owns the transaction and lock ordering.
func PersistTerminalSwissPoints(
	ctx context.Context,
	q *sqlc.Queries,
	in resultrepo.ResultSettlementInput,
	commit sqlc.LockTerminalProjectionCommitRow,
) error {
	rows, err := q.LockTerminalSwissPointSource(ctx, sqlc.LockTerminalSwissPointSourceParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultRevisionID: resultrepo.NullableUUIDValue(commit.SeriesResultRevisionID),
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
	ledger, err := swissusecase.BuildPointLedger(swissusecase.PointLedgerInput{ParticipantIDs: ids, Series: []swissusecase.SeriesPointResult{{
		RoundID: row.RoundID, RoundNumber: int(row.RoundNumber), SeriesID: in.Scope.SeriesID, ResultRevisionID: domain.OfficialResultRevisionID(commit.SeriesResultRevisionID),
		FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID, WinnerID: progressionrepo.ProgressionUUIDPointer(row.WinnerID), Label: label,
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
			RoundID: row.RoundID, RoundNumber: row.RoundNumber, SourceKind: "series", SourceSeriesID: resultrepo.NullableUUIDValue(in.Scope.SeriesID),
			SeriesResultRevisionID: resultrepo.NullableUUIDValue(commit.SeriesResultRevisionID), ResultLabel: resultrepo.OptionalTrimmedString(string(label)),
			//nolint:gosec // Domain validation bounds this value before the storage conversion.
			ParticipantID: award.ParticipantID, OpponentID: resultrepo.NullableUUIDValue(award.OpponentID), Points: int16(award.Points),
			StableSeed: seeds[award.ParticipantID], CreatedAt: resultrepo.TSTZ(in.SettledAt),
		}); err != nil {
			return err
		}
	}
	return nil
}

// MaterializeTerminalSwissStandings creates the canonical standings artifact
// required by a terminal Swiss publication. All source rows are locked in the
// caller's result transaction before the artifact is written.
//
//nolint:gocyclo // Canonical input assembly deliberately validates every source boundary.
func MaterializeTerminalSwissStandings(ctx context.Context, tx *db.TxManager, in resultrepo.ResultSettlementInput) (bool, error) {
	q := tx.Querier(ctx)
	commits, err := q.LockTerminalProjectionCommit(ctx, sqlc.LockTerminalProjectionCommitParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID, ProjectionRevisionID: in.IDs.ProjectionEvidenceID,
	})
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
	input, err := progressionrepo.ProgressionCanonicalSwissInput(
		in.Scope.TournamentID,
		progressionrepo.ProgressionRoundRevisionIDs(rounds, proofs),
		participants,
		ledger,
	)
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
	members := make([]projectionrepo.ProjectionMemberInput, len(artifact.Members))
	for index, member := range artifact.Members {
		members[index] = projectionrepo.ProjectionMemberInput{
			ParticipantID: member.ParticipantID, Position: int32(member.Position), //nolint:gosec // Canonical materialization bounds positions.
			ScoreMilli: member.ScoreMilli,
		}
	}
	dependencies, err := settlementrepo.ParticipantSettlementStandingsDependencies(commits[0].CommandID, settlementLedgerEntries(ledger))
	if err != nil {
		return false, err
	}
	err = projectionrepo.CreateProjectionArtifact(ctx, q,
		projectionrepo.ProjectionPublishInput{
			IDs:   projectionrepo.ProjectionIDs{RevisionID: in.IDs.ProjectionEvidenceID},
			Scope: projectionrepo.ProjectionScope{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID}, CreatedAt: in.SettledAt,
		},
		projectionrepo.ProjectionArtifactInput{
			ID:   uuid.NewSHA1(commits[0].CommandID, []byte("terminal-swiss-standings")),
			Kind: domain.ArtifactKindStandings, Key: "standings", Payload: artifact.Payload,
			PayloadDigest: artifact.PayloadDigest, Members: members, Dependencies: dependencies,
		},
	)
	return err == nil, err
}

func settlementLedgerEntries(rows []sqlc.LockTournamentProgressionSwissLedgerRow) []settlementrepo.SettlementLedgerEntry {
	entries := make([]settlementrepo.SettlementLedgerEntry, len(rows))
	for index, row := range rows {
		entries[index] = settlementrepo.SettlementLedgerEntry{
			RoundID: row.RoundID, RoundNumber: row.RoundNumber,
			SourceKind: row.SourceKind, SourceSeriesID: row.SourceSeriesID,
			SeriesResultRevisionID: row.SeriesResultRevisionID, ByeRevisionID: row.ByeRevisionID,
			ResultLabel: row.ResultLabel, ParticipantID: row.ParticipantID, OpponentID: row.OpponentID,
			Points: row.Points, EffectiveTimeNs: row.EffectiveTimeNs, AcceptedSolveTimeNs: row.AcceptedSolveTimeNs,
			StableSeed: row.StableSeed,
		}
	}
	return entries
}
