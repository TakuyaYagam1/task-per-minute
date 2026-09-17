package result

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
)

// ResultRepository is the narrow settlement boundary required by operator
// result mutations. The shared result adapter remains behind this capability
// port so authority and evidence reads do not depend on its concrete type.
type ResultRepository interface {
	Settle(
		context.Context,
		resultrepo.ResultSettlementInput,
	) (*resultrepo.ResultCommitRecord, bool, error)
}

type TournamentAdminResultPostgres struct {
	tx                   *db.TxManager
	results              ResultRepository
	projectionFinalizer  resultrepo.ProjectionFinalizer
	preStartProofEnsurer PreStartSwissProofEnsurer
}

var _ ResultRepository = (*resultrepo.ResultPostgres)(nil)

func NewTournamentAdminResultPostgres(
	tx *db.TxManager,
	results *resultrepo.ResultPostgres,
) *TournamentAdminResultPostgres {
	var repository ResultRepository
	if results != nil {
		repository = results
	}
	return NewTournamentAdminResultPostgresWithRepository(tx, repository, nil, nil)
}

func NewTournamentAdminResultPostgresWithDependencies(
	tx *db.TxManager,
	results *resultrepo.ResultPostgres,
	projectionFinalizer resultrepo.ProjectionFinalizer,
	preStartProofEnsurer PreStartSwissProofEnsurer,
) *TournamentAdminResultPostgres {
	var repository ResultRepository
	if results != nil {
		repository = results
	}
	return NewTournamentAdminResultPostgresWithRepository(
		tx, repository, projectionFinalizer, preStartProofEnsurer,
	)
}

// NewTournamentAdminResultPostgresWithRepository builds the adapter from the
// consumer-owned settlement boundary while preserving the existing concrete
// constructors above for bootstrap and external callers.
func NewTournamentAdminResultPostgresWithRepository(
	tx *db.TxManager,
	results ResultRepository,
	projectionFinalizer resultrepo.ProjectionFinalizer,
	preStartProofEnsurer PreStartSwissProofEnsurer,
) *TournamentAdminResultPostgres {
	return &TournamentAdminResultPostgres{
		tx:                   tx,
		results:              results,
		projectionFinalizer:  projectionFinalizer,
		preStartProofEnsurer: preStartProofEnsurer,
	}
}

func (r *TournamentAdminResultPostgres) LockOperatorResultAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
) (resultusecase.OperatorResultAuthority, error) {
	if ctx == nil || !r.available() || tournamentID == uuid.Nil || seriesID == uuid.Nil {
		return resultusecase.OperatorResultAuthority{}, domain.ErrValidation
	}
	if err := lockTournamentResultScope(ctx, r.tx.Querier(ctx), tournamentID, uuid.Nil); err != nil {
		return resultusecase.OperatorResultAuthority{}, operatorResultLookupError("lock result scope", err)
	}
	row, err := r.tx.Querier(ctx).LockOperatorResultAuthority(
		ctx,
		sqlc.LockOperatorResultAuthorityParams{TournamentID: tournamentID, SeriesID: seriesID},
	)
	if err != nil {
		return resultusecase.OperatorResultAuthority{}, operatorResultLookupError("lock authority", err)
	}
	authority := resultusecase.OperatorResultAuthority{
		TournamentID: row.TournamentID, RosterID: row.RosterID, SeriesID: row.SeriesID,
		TournamentState:   domain.TournamentState(row.TournamentState),
		AuthorityRevision: row.AuthorityRevision, ProjectionRevisionID: row.ProjectionRevisionID,
		ProjectionRevision: row.ProjectionRevision,
	}
	if !validOperatorResultAuthority(authority, tournamentID, seriesID) {
		return resultusecase.OperatorResultAuthority{}, domain.ErrInternal
	}
	return authority, nil
}

func (r *TournamentAdminResultPostgres) FindOperatorResultCommand(
	ctx context.Context,
	commandID uuid.UUID,
) (*resultusecase.OperatorResultCommandRecord, error) {
	if ctx == nil || !r.available() || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetOperatorResultCommand(ctx, commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminResultPostgres - find command: %w", err)
	}
	record, err := operatorResultCommandRecord(row)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *TournamentAdminResultPostgres) ReadOperatorResultTime(ctx context.Context) (time.Time, error) {
	if ctx == nil || !r.available() {
		return time.Time{}, domain.ErrValidation
	}
	value, err := r.tx.Querier(ctx).GetOperatorResultTime(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("TournamentAdminResultPostgres - read DB time: %w", err)
	}
	result := value.Time.Round(0).UTC()
	if !value.Valid || !domain.IsValidServerTime(result) {
		return time.Time{}, domain.ErrInternal
	}
	return result, nil
}

func (r *TournamentAdminResultPostgres) available() bool {
	return r != nil && r.tx != nil && r.results != nil
}

func operatorResultCommandRecord(row sqlc.OperatorResultCommand) (resultusecase.OperatorResultCommandRecord, error) {
	if len(row.RequestDigest) != 32 || !row.ExecutedAt.Valid || row.RosterID == uuid.Nil {
		return resultusecase.OperatorResultCommandRecord{}, domain.ErrInternal
	}
	record := resultusecase.OperatorResultCommandRecord{
		CommandScope: adminoperation.CommandScope{
			Operator:     adminoperation.OperatorIdentity{ActorID: row.ActorID},
			TournamentID: row.TournamentID, CommandID: row.CommandID,
		},
		SeriesID: row.SeriesID, Action: resultusecase.OperatorResultAction(row.Action),
		ExpectedAuthorityRevision: row.ExpectedAuthorityRevision,
		CommitID:                  row.CommitID, ResultEventID: row.ResultEventID,
		ExecutedAt: row.ExecutedAt.Time.Round(0).UTC(),
	}
	copy(record.RequestDigest[:], row.RequestDigest)
	return record, nil
}

func validOperatorResultAuthority(
	authority resultusecase.OperatorResultAuthority,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
) bool {
	return authority.TournamentID == tournamentID && authority.RosterID != uuid.Nil &&
		authority.SeriesID == seriesID && authority.TournamentState.IsValid() &&
		authority.AuthorityRevision >= 1 && authority.ProjectionRevisionID != uuid.Nil &&
		authority.ProjectionRevision >= 1
}

func operatorResultLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return fmt.Errorf("TournamentAdminResultPostgres - %s: %w", operation, err)
}

var _ resultusecase.OperatorResultWorkflowRepository = (*TournamentAdminResultPostgres)(nil)
