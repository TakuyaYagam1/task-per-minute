package application

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	admininbound "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

func (a *AdminUseCase) ListTournaments(
	ctx context.Context,
	command usecase.TournamentListCommand,
) (usecase.TournamentPage, error) {
	if ctx == nil {
		return usecase.TournamentPage{}, domain.ErrValidation
	}
	if a == nil || a.catalog == nil {
		return usecase.TournamentPage{}, domain.ErrInternal
	}
	page, err := a.catalog.ListTournaments(ctx, command)
	if err != nil {
		return usecase.TournamentPage{}, err
	}
	if !validTournamentPage(page) {
		return usecase.TournamentPage{}, domain.ErrInternal
	}
	return page, nil
}

func (a *AdminUseCase) CreateTournament(
	ctx context.Context,
	command usecase.TournamentCreateCommand,
) (usecase.TournamentResult, error) {
	if ctx == nil {
		return usecase.TournamentResult{}, domain.ErrValidation
	}
	if a == nil || a.catalog == nil {
		return usecase.TournamentResult{}, domain.ErrInternal
	}
	result, err := a.catalog.CreateTournament(ctx, command)
	if err != nil {
		return usecase.TournamentResult{}, err
	}
	if result.Tournament.Preset != command.Preset || !lifecycleusecase.ValidTournamentView(result.Tournament, result.Tournament.ID) {
		return usecase.TournamentResult{}, domain.ErrInternal
	}
	return result, nil
}

func (a *AdminUseCase) GetTournamentContent(
	ctx context.Context,
	operator usecase.OperatorIdentity,
) (usecase.TournamentContentView, error) {
	if ctx == nil || operator.ActorID == uuid.Nil {
		return usecase.TournamentContentView{}, domain.ErrValidation
	}
	if a == nil || a.catalog == nil {
		return usecase.TournamentContentView{}, domain.ErrInternal
	}
	return a.catalog.GetTournamentContent(ctx, operator)
}

func validTournamentPage(page usecase.TournamentPage) bool {
	if page.Items == nil {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(page.Items))
	for _, item := range page.Items {
		if !lifecycleusecase.ValidTournamentView(item, item.ID) || !addUniqueID(seen, item.ID) {
			return false
		}
	}
	return page.Next == nil || page.Next.TournamentID != uuid.Nil && domain.IsValidServerTime(page.Next.CreatedAt)
}

func normalizeAdminError(err error) error {
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		return err
	}
	// Exact assignment exhaustion is a content failure, not a stale operator revision.
	if errors.Is(err, assignmentusecase.ErrInvalidExactNormalAssignment) {
		return domain.ErrInvalidContentConfiguration
	}
	var conflict *operationusecase.RevisionConflictError
	if !errors.As(err, &conflict) || conflict.ExpectedRevision < 1 || conflict.CurrentRevision < 1 ||
		conflict.CurrentState != "" && !conflict.CurrentState.IsValid() {
		return domain.ErrInternal
	}
	return err
}

func addUniqueID(values map[uuid.UUID]struct{}, value uuid.UUID) bool {
	if _, exists := values[value]; exists {
		return false
	}
	values[value] = struct{}{}
	return true
}

var (
	_ admininbound.AdminService = (*AdminUseCase)(nil)
	_ usecase.TournamentUseCase = (*AdminUseCase)(nil)
)
