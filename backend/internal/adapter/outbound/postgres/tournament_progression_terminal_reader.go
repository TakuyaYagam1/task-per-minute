package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// LoadLockedSwissTerminalEvidence rebuilds canonical Final Swiss input from
// the immutable receipt chain. It intentionally never reads a mutable result,
// score, game, or correction head: every identity below is stored by the
// receipt that admitted the Final Swiss revision.
func (r *TournamentProgressionPostgres) LoadLockedSwissTerminalEvidence(
	ctx context.Context,
	command tournamentprogression.Command,
	authority tournamentprogression.Authority,
) (playoff.ProgressionSwissInput, error) {
	if r == nil || r.tx == nil || ctx == nil || !validTournamentProgressionCommand(command) ||
		!validTournamentProgressionAuthority(authority) || command.TournamentID != authority.Tournament.ID ||
		command.RosterID != authority.Tournament.RosterID {
		return playoff.ProgressionSwissInput{}, domain.ErrValidation
	}

	var input playoff.ProgressionSwissInput
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		rows, err := r.lockFinalSwissReceiptRows(txCtx, authority)
		if err != nil {
			return err
		}
		input, err = progressionSwissInputFromReceipt(command, authority, rows)
		return err
	})
	if err != nil {
		return playoff.ProgressionSwissInput{}, err
	}
	return input, nil
}

func progressionGoldenIdentities(authority tournamentprogression.Authority, rows []sqlc.LockTournamentProgressionGoldenSettlementsRow) ([]playoff.FinalSwissGoldenGroupIdentity, error) {
	if len(rows) == 0 {
		return nil, domain.ErrConflict
	}
	identities := make([]playoff.FinalSwissGoldenGroupIdentity, 0)
	byID := make(map[uuid.UUID]playoff.FinalSwissGoldenGroupIdentity)
	for _, row := range rows {
		if row.GroupID == uuid.Nil || row.GroupRevisionID == uuid.Nil || row.SourceProjectionRevisionID != authority.ProjectionRevisionID || row.SourceProjectionRevision != authority.ProjectionRevision || row.PositionFrom < 1 || row.PositionTo < row.PositionFrom || int(row.PositionTo) > domain.TournamentMaxParticipants {
			return nil, domain.ErrConflict
		}
		identity := playoff.FinalSwissGoldenGroupIdentity{GroupID: row.GroupID, RevisionID: domain.DerivedRevisionID(row.GroupRevisionID), PositionFrom: int(row.PositionFrom), PositionTo: int(row.PositionTo)}
		if previous, found := byID[row.GroupID]; found {
			if previous != identity {
				return nil, domain.ErrConflict
			}
			continue
		}
		byID[row.GroupID] = identity
		identities = append(identities, identity)
	}
	return identities, nil
}

type progressionFinalSwissReceiptRows struct {
	goldenGroups           []sqlc.LockTournamentProgressionGoldenSettlementsRow
	chain                  []sqlc.LockTournamentProgressionFinalSwissReceiptChainRow
	participants           []sqlc.LockTournamentProgressionFinalSwissReceiptParticipantsRow
	rounds                 []sqlc.LockTournamentProgressionFinalSwissReceiptRoundsRow
	series                 []sqlc.LockTournamentProgressionFinalSwissReceiptSeriesRow
	games                  []sqlc.LockTournamentProgressionFinalSwissReceiptGamesRow
	ledgerEntries          []sqlc.LockTournamentProgressionFinalSwissReceiptLedgerEntriesRow
	seriesEvidence         []sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow
	scoreAttempts          []sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAttemptsRow
	scoreAdjudications     []sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudicationsRow
	gameEvidence           []sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow
	normalNoShowCommits    []sqlc.LockTournamentProgressionFinalSwissReceiptNormalNoShowCommitsRow
	operatorForfeitCommits []sqlc.LockTournamentProgressionFinalSwissReceiptOperatorForfeitCommitsRow
	nodes                  []sqlc.LockTournamentProgressionFinalSwissReceiptProjectionNodesRow
	dependencies           []sqlc.LockTournamentProgressionFinalSwissReceiptProjectionDependenciesRow
	ledger                 []sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow
	sourceProjections      []sqlc.LockTournamentProgressionFinalSwissReceiptSourceProjectionsRow
	logicalNodes           []sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow
	lockProofs             []sqlc.SwissRoundLockProof
	lockProofMembers       []sqlc.SwissRoundLockProofMember
	lockProofSeries        []sqlc.SwissRoundLockProofSeries
}

func (r *TournamentProgressionPostgres) lockFinalSwissReceiptRows(
	ctx context.Context,
	authority tournamentprogression.Authority,
) (progressionFinalSwissReceiptRows, error) {
	querier := r.tx.Querier(ctx)
	params := sqlc.LockTournamentProgressionFinalSwissReceiptChainParams{
		ProjectionRevisionID: authority.ProjectionRevisionID,
		TournamentID:         authority.Tournament.ID,
		RosterID:             authority.Tournament.RosterID,
	}
	rows := progressionFinalSwissReceiptRows{}
	var err error
	if rows.chain, err = querier.LockTournamentProgressionFinalSwissReceiptChain(ctx, params); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt chain", err)
	}
	if authority.Tournament.State == domain.TournamentStateGolden {
		for _, receipt := range rows.chain {
			groups, err := querier.LockTournamentProgressionGoldenSettlements(ctx, sqlc.LockTournamentProgressionGoldenSettlementsParams{
				TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
				SourceProjectionRevisionID: receipt.ProjectionRevisionID, SourceProjectionRevision: receipt.PhysicalProjectionRevision,
			})
			if err != nil {
				return rows, tournamentProgressionReadError("read receipt Golden identities", err)
			}
			if receipt.ProjectionRevisionID == authority.ProjectionRevisionID && len(groups) == 0 {
				return rows, domain.ErrConflict
			}
			rows.goldenGroups = append(rows.goldenGroups, groups...)
		}
	}
	// Every child query is parameterized by the same immutable receipt root.
	// The query sources retain the database lock ordering within their joins.
	child := func() (sqlc.LockTournamentProgressionFinalSwissReceiptParticipantsParams, error) {
		if len(rows.chain) == 0 {
			return sqlc.LockTournamentProgressionFinalSwissReceiptParticipantsParams{}, domain.ErrConflict
		}
		return sqlc.LockTournamentProgressionFinalSwissReceiptParticipantsParams{
			ProjectionRevisionID: authority.ProjectionRevisionID,
			TournamentID:         authority.Tournament.ID,
			RosterID:             authority.Tournament.RosterID,
		}, nil
	}
	participantParams, err := child()
	if err != nil {
		return rows, err
	}
	if rows.participants, err = querier.LockTournamentProgressionFinalSwissReceiptParticipants(ctx, participantParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt participants", err)
	}
	roundParams := sqlc.LockTournamentProgressionFinalSwissReceiptRoundsParams(participantParams)
	if rows.rounds, err = querier.LockTournamentProgressionFinalSwissReceiptRounds(ctx, roundParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt rounds", err)
	}
	seriesParams := sqlc.LockTournamentProgressionFinalSwissReceiptSeriesParams(participantParams)
	if rows.series, err = querier.LockTournamentProgressionFinalSwissReceiptSeries(ctx, seriesParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt Series", err)
	}
	gameParams := sqlc.LockTournamentProgressionFinalSwissReceiptGamesParams(participantParams)
	if rows.games, err = querier.LockTournamentProgressionFinalSwissReceiptGames(ctx, gameParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt Games", err)
	}
	ledgerEntryParams := sqlc.LockTournamentProgressionFinalSwissReceiptLedgerEntriesParams(participantParams)
	if rows.ledgerEntries, err = querier.LockTournamentProgressionFinalSwissReceiptLedgerEntries(ctx, ledgerEntryParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt ledger entries", err)
	}
	seriesEvidenceParams := sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceParams(participantParams)
	if rows.seriesEvidence, err = querier.LockTournamentProgressionFinalSwissReceiptSeriesEvidence(ctx, seriesEvidenceParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt Series evidence", err)
	}
	scoreAttemptParams := sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAttemptsParams(participantParams)
	if rows.scoreAttempts, err = querier.LockTournamentProgressionFinalSwissReceiptScoreRevisionAttempts(ctx, scoreAttemptParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt score attempts", err)
	}
	scoreAdjudicationParams := sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudicationsParams(participantParams)
	if rows.scoreAdjudications, err = querier.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudications(ctx, scoreAdjudicationParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt score adjudications", err)
	}
	gameEvidenceParams := sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceParams(participantParams)
	if rows.gameEvidence, err = querier.LockTournamentProgressionFinalSwissReceiptGameEvidence(ctx, gameEvidenceParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt Game evidence", err)
	}
	normalParams := sqlc.LockTournamentProgressionFinalSwissReceiptNormalNoShowCommitsParams(participantParams)
	if rows.normalNoShowCommits, err = querier.LockTournamentProgressionFinalSwissReceiptNormalNoShowCommits(ctx, normalParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt normal no-show commits", err)
	}
	forfeitParams := sqlc.LockTournamentProgressionFinalSwissReceiptOperatorForfeitCommitsParams(participantParams)
	if rows.operatorForfeitCommits, err = querier.LockTournamentProgressionFinalSwissReceiptOperatorForfeitCommits(ctx, forfeitParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt operator forfeits", err)
	}
	nodeParams := sqlc.LockTournamentProgressionFinalSwissReceiptProjectionNodesParams(participantParams)
	if rows.nodes, err = querier.LockTournamentProgressionFinalSwissReceiptProjectionNodes(ctx, nodeParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt nodes", err)
	}
	dependencyParams := sqlc.LockTournamentProgressionFinalSwissReceiptProjectionDependenciesParams(participantParams)
	if rows.dependencies, err = querier.LockTournamentProgressionFinalSwissReceiptProjectionDependencies(ctx, dependencyParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt dependencies", err)
	}
	ledgerParams := sqlc.LockTournamentProgressionFinalSwissReceiptLedgerParams(participantParams)
	if rows.ledger, err = querier.LockTournamentProgressionFinalSwissReceiptLedger(ctx, ledgerParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt ledger", err)
	}
	sourceParams := sqlc.LockTournamentProgressionFinalSwissReceiptSourceProjectionsParams{
		TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
		ProjectionRevisionID: authority.ProjectionRevisionID,
	}
	if rows.sourceProjections, err = querier.LockTournamentProgressionFinalSwissReceiptSourceProjections(ctx, sourceParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt source projections", err)
	}
	logicalParams := sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesParams{
		TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
		ProjectionRevisionID: authority.ProjectionRevisionID,
	}
	if rows.logicalNodes, err = querier.LockTournamentProgressionFinalSwissReceiptLogicalResultNodes(ctx, logicalParams); err != nil {
		return rows, tournamentProgressionReadError("lock Final Swiss receipt logical nodes", err)
	}
	proofParams := sqlc.LockTournamentProgressionSwissRoundLockProofsParams{
		TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
	}
	if rows.lockProofs, err = querier.LockTournamentProgressionSwissRoundLockProofs(ctx, proofParams); err != nil {
		return rows, tournamentProgressionReadError("lock Swiss proof roots", err)
	}
	membersParams := sqlc.LockTournamentProgressionSwissRoundLockProofMembersParams(proofParams)
	if rows.lockProofMembers, err = querier.LockTournamentProgressionSwissRoundLockProofMembers(ctx, membersParams); err != nil {
		return rows, tournamentProgressionReadError("lock Swiss proof members", err)
	}
	seriesProofParams := sqlc.LockTournamentProgressionSwissRoundLockProofSeriesParams(proofParams)
	if rows.lockProofSeries, err = querier.LockTournamentProgressionSwissRoundLockProofSeries(ctx, seriesProofParams); err != nil {
		return rows, tournamentProgressionReadError("lock Swiss proof Series", err)
	}
	return rows, nil
}

func validTournamentProgressionCommand(command tournamentprogression.Command) bool {
	return command.CommandID != uuid.Nil && command.TournamentID != uuid.Nil && command.RosterID != uuid.Nil &&
		command.ActorID != uuid.Nil && command.ExpectedProjectionRevision >= 1 &&
		(command.Action == tournamentprogression.ActionStartGolden || command.Action == tournamentprogression.ActionStartPlayoffs)
}
