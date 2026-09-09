package postgres

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func (r *TournamentProgressionPostgres) LoadGoldenEvidence(ctx context.Context, authority tournamentprogression.Authority) (tournamentprogression.GoldenEvidence, error) {
	if r == nil || r.tx == nil || ctx == nil || !validTournamentProgressionAuthority(authority) || authority.Tournament.State != domain.TournamentStateGolden {
		return tournamentprogression.GoldenEvidence{}, domain.ErrValidation
	}
	var result tournamentprogression.GoldenEvidence
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		result.Swiss, err = r.loadSwissEvidence(txCtx, authority, func(lockCtx context.Context) error {
			result.Settlements, err = r.loadGoldenSettlements(lockCtx, authority)
			return err
		})
		if err != nil {
			return err
		}
		rows, err := r.lockFinalSwissReceiptRows(txCtx, authority)
		if err != nil {
			return err
		}
		input, err := progressionSwissInputFromReceipt(tournamentprogression.Command{}, authority, rows)
		if err != nil {
			return err
		}
		for _, round := range input.Rounds {
			result.CurrentTerminalSeries = append(result.CurrentTerminalSeries, round.Series...)
		}
		if len(result.CurrentTerminalSeries) != result.Swiss.ExpectedSeries {
			return domain.ErrConflict
		}
		return nil
	})
	if err != nil {
		return tournamentprogression.GoldenEvidence{}, err
	}
	return result, nil
}

func (r *TournamentProgressionPostgres) loadGoldenSettlements(ctx context.Context, authority tournamentprogression.Authority) ([]playoff.Top4GoldenSettlement, error) {
	q := r.tx.Querier(ctx)
	p := sqlc.LockTournamentProgressionGoldenSettlementsParams{
		TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
		SourceProjectionRevisionID: authority.ProjectionRevisionID, SourceProjectionRevision: authority.ProjectionRevision,
	}
	groups, err := q.LockTournamentProgressionGoldenSettlements(ctx, p)
	if err != nil {
		return nil, tournamentProgressionReadError("lock Golden groups", err)
	}
	attempts, err := q.LockTournamentProgressionGoldenAttempts(ctx, sqlc.LockTournamentProgressionGoldenAttemptsParams(p))
	if err != nil {
		return nil, tournamentProgressionReadError("lock Golden attempts", err)
	}
	commits, err := q.LockTournamentProgressionGoldenPositionCommits(ctx, sqlc.LockTournamentProgressionGoldenPositionCommitsParams(p))
	if err != nil {
		return nil, tournamentProgressionReadError("lock Golden commits", err)
	}
	ledger, err := q.LockTournamentProgressionGoldenPositionLedger(ctx, sqlc.LockTournamentProgressionGoldenPositionLedgerParams{TournamentID: p.TournamentID, RosterID: p.RosterID})
	if err != nil {
		return nil, tournamentProgressionReadError("lock Golden ledger", err)
	}
	seals, err := q.LockTournamentProgressionGoldenPositionLedgerRevisionSeals(ctx, sqlc.LockTournamentProgressionGoldenPositionLedgerRevisionSealsParams{TournamentID: p.TournamentID, RosterID: p.RosterID})
	if err != nil {
		return nil, tournamentProgressionReadError("lock Golden seals", err)
	}
	return progressionGoldenSettlements(authority, groups, attempts, commits, ledger, seals)
}
