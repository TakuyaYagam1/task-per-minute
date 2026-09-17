package admin

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

const maxReasonRunes = 512

// Service is the transport-neutral operator boundary for one tournament.
// Actor identity is always derived from the authenticated session by the
// inbound adapter; no command trusts a client-provided operator identity.
type AdminService interface {
	GetRoster(ctx context.Context, query RosterQuery) (RosterView, error)
	ReplaceRoster(ctx context.Context, command ReplaceRosterCommand) (RosterView, error)
	RunPreflight(ctx context.Context, command PreflightCommand) (tournamentpreflight.ReportRevision, error)
	LockRoster(ctx context.Context, command LockRosterCommand) (RosterView, error)
	UnlockRoster(ctx context.Context, command UnlockRosterCommand) (RosterView, error)
	ConfigurePairings(ctx context.Context, command PairingCommand) (SwissRoundView, error)
	ApplyTournamentAction(ctx context.Context, command TournamentActionCommand) (usecase.TournamentView, error)
	ControlWave(ctx context.Context, command WaveCommand) (WaveView, error)
	ResolveNoShow(ctx context.Context, command NoShowCommand) error
	AssignReserve(ctx context.Context, command ReserveCommand) error
	RecordForfeit(ctx context.Context, command ForfeitCommand) error
	ReplayGame(ctx context.Context, command ReplayCommand) error
	CorrectGameResult(ctx context.Context, command CorrectionCommand) (CorrectionEvidence, error)
	ListAudit(ctx context.Context, query incidentusecase.AuditQuery) (audit.AuditPage, error)
	ExportIncident(ctx context.Context, query incidentusecase.IncidentQuery) (audit.IncidentBundle, error)
	GetOperatorSnapshot(ctx context.Context, query SnapshotQuery) (OperatorSnapshotView, error)
}

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
	if result.Tournament.Preset != command.Preset || !validTournamentView(result.Tournament, result.Tournament.ID) {
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
		if !validTournamentView(item, item.ID) || !addUniqueID(seen, item.ID) {
			return false
		}
	}
	return page.Next == nil || page.Next.TournamentID != uuid.Nil && domain.IsValidServerTime(page.Next.CreatedAt)
}

func validOperator(operator OperatorIdentity) bool {
	return operator.ActorID != uuid.Nil
}

func validCommandScope(scope CommandScope) bool {
	return validOperator(scope.Operator) && scope.TournamentID != uuid.Nil && scope.CommandID != uuid.Nil
}

func normalizeAdminError(err error) error {
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		return err
	}
	var conflict *RevisionConflictError
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

func addUniqueInt(values map[int]struct{}, value int) bool {
	if _, exists := values[value]; exists {
		return false
	}
	values[value] = struct{}{}
	return true
}

func containsID(values map[uuid.UUID]struct{}, value uuid.UUID) bool {
	_, exists := values[value]
	return exists
}

func idSet(values []uuid.UUID) map[uuid.UUID]struct{} {
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func validUniqueIDs(values []uuid.UUID, minimum, maximum int) bool {
	if len(values) < minimum || len(values) > maximum {
		return false
	}
	canonical := append([]uuid.UUID(nil), values...)
	slices.SortFunc(canonical, func(first, second uuid.UUID) int { return bytes.Compare(first[:], second[:]) })
	for index, value := range canonical {
		if value == uuid.Nil || index > 0 && value == canonical[index-1] {
			return false
		}
	}
	return true
}

func validEventTime(value *time.Time, createdAt, updatedAt time.Time) bool {
	return value == nil || domain.IsValidServerTime(*value) && !value.Before(createdAt) && !value.After(updatedAt)
}

func validText(value string, maxRunes int) bool {
	trimmed := strings.TrimSpace(value)
	return utf8.ValidString(value) && trimmed != "" && trimmed == value && utf8.RuneCountInString(value) <= maxRunes
}

func validOptionalText(value string, maxRunes int) bool {
	return value == "" || validText(value, maxRunes)
}

var _ usecase.TournamentUseCase = (*AdminUseCase)(nil)
