package postgres

import (
	"context"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/google/uuid"
	"time"
)

func loadFinalSwissPublicationChildren(ctx context.Context, q *sqlc.Queries, scope ProjectionScope, receiptID uuid.UUID, now time.Time, rows *progressionFinalSwissReceiptRows) error {
	ledger, err := q.LockTournamentProgressionAllSwissLedger(ctx, sqlc.LockTournamentProgressionAllSwissLedgerParams(scope))
	if err != nil {
		return err
	}
	for _, row := range ledger {
		rows.ledger = append(rows.ledger, sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow{
			ProjectionRevisionID:   receiptID,
			LedgerEntryID:          row.ID,
			RoundID:                row.RoundID,
			RoundNumber:            row.RoundNumber,
			SourceKind:             row.SourceKind,
			SourceSeriesID:         row.SourceSeriesID,
			SeriesResultRevisionID: row.SeriesResultRevisionID,
			ByeRevisionID:          row.ByeRevisionID,
			ResultLabel:            row.ResultLabel,
			ParticipantID:          row.ParticipantID,
			OpponentID:             row.OpponentID,
			Points:                 row.Points,
			EffectiveTimeNs:        row.EffectiveTimeNs,
			AcceptedSolveTimeNs:    row.AcceptedSolveTimeNs,
			StableSeed:             row.StableSeed,
			CreatedAt:              tstz(now),
		})
	}
	scoreAttempts, err := q.LockTournamentProgressionSwissScoreRevisionAttempts(ctx, sqlc.LockTournamentProgressionSwissScoreRevisionAttemptsParams(scope))
	if err != nil {
		return err
	}
	for _, row := range scoreAttempts {
		rows.scoreAttempts = append(rows.scoreAttempts, sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAttemptsRow{
			ProjectionRevisionID: receiptID,
			RoundID:              row.RoundID,
			SeriesID:             row.SeriesID,
			ScoreRevisionID:      row.ScoreRevisionID,
			Position:             row.Position,
			SlotID:               row.SlotID,
			SlotPosition:         row.SlotPosition,
			GameAttemptID:        row.GameAttemptID,
			AttemptNumber:        row.AttemptNumber,
			GameResultRevisionID: row.GameResultRevisionID,
			ResultEventID:        row.ResultEventID,
			ResultState:          row.ResultState,
			ResultReason:         row.ResultReason,
			WinnerID:             row.WinnerID,
			OccurredAt:           row.OccurredAt,
			CreatedAt:            row.CreatedAt,
		})
	}
	scoreAdjudications, err := q.LockTournamentProgressionSwissScoreRevisionAdjudications(ctx, sqlc.LockTournamentProgressionSwissScoreRevisionAdjudicationsParams(scope))
	if err != nil {
		return err
	}
	for _, row := range scoreAdjudications {
		rows.scoreAdjudications = append(rows.scoreAdjudications, sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudicationsRow{
			ProjectionRevisionID:       receiptID,
			RoundID:                    row.RoundID,
			SeriesID:                   row.SeriesID,
			ScoreRevisionID:            row.ScoreRevisionID,
			OperatorForfeitCommitID:    row.OperatorForfeitCommitID,
			CommandID:                  row.CommandID,
			ActorID:                    row.ActorID,
			AnchorAttemptID:            row.AnchorAttemptID,
			ForfeitingParticipantID:    row.ForfeitingParticipantID,
			WinnerID:                   row.WinnerID,
			SourceProjectionRevisionID: row.SourceProjectionRevisionID,
			SourceProjectionRevision:   row.SourceProjectionRevision,
			CreatedAt:                  row.CreatedAt,
		})
	}
	normalNoShowCommits, err := q.LockTournamentProgressionSwissNormalNoShowCommits(ctx, sqlc.LockTournamentProgressionSwissNormalNoShowCommitsParams(scope))
	if err != nil {
		return err
	}
	for _, row := range normalNoShowCommits {
		rows.normalNoShowCommits = append(rows.normalNoShowCommits, sqlc.LockTournamentProgressionFinalSwissReceiptNormalNoShowCommitsRow{
			ProjectionRevisionID:   receiptID,
			RoundID:                row.RoundID,
			SeriesID:               row.SeriesID,
			CommitID:               row.CommitID,
			WaveID:                 row.WaveID,
			ReadyWindowID:          row.ReadyWindowID,
			ReadyWindowRevisionID:  row.ReadyWindowRevisionID,
			ResultEventID:          row.ResultEventID,
			SeriesScoreRevisionID:  row.SeriesScoreRevisionID,
			SeriesResultRevisionID: row.SeriesResultRevisionID,
			CommandID:              row.CommandID,
			Action:                 row.Action,
			ResolvedAt:             row.ResolvedAt,
			GameAttemptID:          row.GameAttemptID,
			GameResultRevisionID:   row.GameResultRevisionID,
			Position:               row.Position,
			ArtifactKinds:          row.ArtifactKinds,
			PayloadDigest:          row.PayloadDigest,
		})
	}
	operatorForfeitCommits, err := q.LockTournamentProgressionSwissOperatorForfeitCommits(ctx, sqlc.LockTournamentProgressionSwissOperatorForfeitCommitsParams(scope))
	if err != nil {
		return err
	}
	for _, row := range operatorForfeitCommits {
		rows.operatorForfeitCommits = append(rows.operatorForfeitCommits, sqlc.LockTournamentProgressionFinalSwissReceiptOperatorForfeitCommitsRow{
			ProjectionRevisionID:       receiptID,
			RoundID:                    row.RoundID,
			SeriesID:                   row.SeriesID,
			CommitID:                   row.CommitID,
			AnchorAttemptID:            row.AnchorAttemptID,
			ResultEventID:              row.ResultEventID,
			SeriesScoreRevisionID:      row.SeriesScoreRevisionID,
			SeriesResultRevisionID:     row.SeriesResultRevisionID,
			CommandID:                  row.CommandID,
			ActorID:                    row.ActorID,
			ForfeitingParticipantID:    row.ForfeitingParticipantID,
			SourceProjectionRevisionID: row.SourceProjectionRevisionID,
			SourceProjectionRevision:   row.SourceProjectionRevision,
			RuleID:                     row.RuleID,
			Reason:                     row.Reason,
			EvidenceIds:                row.EvidenceIds,
			ResolvedAt:                 row.ResolvedAt,
			ArtifactKinds:              row.ArtifactKinds,
			PayloadDigest:              row.PayloadDigest,
		})
	}
	dependencies, err := q.LockTournamentProgressionSwissProjectionDependencies(ctx, sqlc.LockTournamentProgressionSwissProjectionDependenciesParams(scope))
	if err != nil {
		return err
	}
	for _, row := range dependencies {
		rows.dependencies = append(rows.dependencies, sqlc.LockTournamentProgressionFinalSwissReceiptProjectionDependenciesRow(row))
	}
	return nil
}
