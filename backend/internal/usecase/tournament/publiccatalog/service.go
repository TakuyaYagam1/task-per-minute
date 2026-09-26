package publiccatalog

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 50
	MaxSearchLength = 80
)

var publicIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) ListPublicTournaments(
	ctx context.Context,
	query inbound.PublicTournamentCatalogQuery,
) (inbound.PublicTournamentCatalogPage, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return inbound.PublicTournamentCatalogPage{}, domain.ErrInternal
	}
	normalized, err := normalizeQuery(query)
	if err != nil {
		return inbound.PublicTournamentCatalogPage{}, err
	}
	page, err := s.repository.ListPublicTournaments(ctx, normalized)
	if err != nil {
		return inbound.PublicTournamentCatalogPage{}, err
	}
	if len(page.Items) > normalized.Limit {
		return inbound.PublicTournamentCatalogPage{}, domain.ErrInternal
	}

	items := make([]inbound.PublicTournamentCatalogView, 0, len(page.Items))
	for _, record := range page.Items {
		view, mapErr := publicTournamentView(record)
		if mapErr != nil {
			return inbound.PublicTournamentCatalogPage{}, mapErr
		}
		items = append(items, view)
	}

	result := inbound.PublicTournamentCatalogPage{Items: items}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		if _, err := publicTournamentView(last); err != nil {
			return inbound.PublicTournamentCatalogPage{}, err
		}
		result.Next = &inbound.PublicTournamentCatalogCursor{
			Search:       normalized.Search,
			Group:        normalized.Group,
			Sort:         normalized.Sort,
			GroupRank:    groupRank(last.Group),
			Name:         last.OrderName,
			CreatedAt:    last.CreatedAt,
			TournamentID: last.TournamentID,
		}
	}
	return result, nil
}

func (s *Service) GetPublicTournamentByPublicID(
	ctx context.Context,
	publicID string,
) (inbound.PublicTournamentCatalogView, error) {
	if ctx == nil || s == nil || s.repository == nil {
		return inbound.PublicTournamentCatalogView{}, domain.ErrInternal
	}
	if !validPublicID(publicID) {
		return inbound.PublicTournamentCatalogView{}, domain.ErrValidation
	}
	record, err := s.repository.GetPublicTournamentByPublicID(ctx, publicID)
	if err != nil {
		return inbound.PublicTournamentCatalogView{}, err
	}
	return publicTournamentView(record)
}

func normalizeQuery(query inbound.PublicTournamentCatalogQuery) (inbound.PublicTournamentCatalogQuery, error) {
	query.Search = strings.ToLower(strings.TrimSpace(query.Search))
	if len([]rune(query.Search)) > MaxSearchLength {
		return inbound.PublicTournamentCatalogQuery{}, domain.ErrValidation
	}
	if query.Group == "" {
		query.Group = inbound.PublicTournamentCatalogFilterAll
	}
	if query.Sort == "" {
		query.Sort = inbound.PublicTournamentCatalogSortActivity
	}
	if !query.Group.IsValid() || !query.Sort.IsValid() {
		return inbound.PublicTournamentCatalogQuery{}, domain.ErrValidation
	}
	if query.Limit == 0 {
		query.Limit = DefaultPageSize
	}
	if query.Limit < 1 || query.Limit > MaxPageSize {
		return inbound.PublicTournamentCatalogQuery{}, domain.ErrValidation
	}
	if query.After != nil {
		if err := validateCursor(query, *query.After); err != nil {
			return inbound.PublicTournamentCatalogQuery{}, err
		}
	}
	return query, nil
}

func validateCursor(query inbound.PublicTournamentCatalogQuery, cursor inbound.PublicTournamentCatalogCursor) error {
	if cursor.Search != query.Search || cursor.Group != query.Group || cursor.Sort != query.Sort ||
		cursor.TournamentID == uuid.Nil || !domain.IsValidServerTime(cursor.CreatedAt) ||
		cursor.GroupRank < 0 || cursor.GroupRank > 2 {
		return domain.ErrValidation
	}
	if query.Sort == inbound.PublicTournamentCatalogSortName && cursor.Name == "" {
		return domain.ErrValidation
	}
	return nil
}

func validPublicID(publicID string) bool {
	return len(publicID) >= 1 && len(publicID) <= 64 && publicIDPattern.MatchString(publicID)
}

func publicTournamentView(record PublicTournamentRecord) (inbound.PublicTournamentCatalogView, error) {
	if err := validatePublicTournamentRecord(record); err != nil {
		return inbound.PublicTournamentCatalogView{}, domain.ErrInternal
	}
	return inbound.PublicTournamentCatalogView{
		TournamentID:      record.TournamentID,
		PublicID:          record.PublicID,
		Name:              record.Name,
		Preset:            record.Preset,
		State:             record.State,
		Group:             record.Group,
		Stage:             record.Stage,
		PlannedRosterSize: record.PlannedRosterSize,
		RosterSize:        record.RosterSize,
		CreatedAt:         record.CreatedAt,
		StartedAt:         cloneTime(record.StartedAt),
		FinishedAt:        cloneTime(record.FinishedAt),
		ScheduledAt:       cloneTime(record.ScheduledAt),
	}, nil
}

func validatePublicTournamentRecord(record PublicTournamentRecord) error {
	if err := validatePublicTournamentIdentity(record); err != nil {
		return err
	}
	if err := validatePublicTournamentSizes(record); err != nil {
		return err
	}
	if err := validatePublicTournamentTimes(record); err != nil {
		return err
	}
	if !publicStateMatchesGroup(record.State, record.Group) || !publicStageMatchesState(record.State, record.Stage) {
		return domain.ErrInternal
	}
	return nil
}

func validatePublicTournamentIdentity(record PublicTournamentRecord) error {
	if record.TournamentID == uuid.Nil || !validPublicID(record.PublicID) ||
		record.Name == "" || len([]rune(record.Name)) > 120 || record.OrderName == "" ||
		record.Preset != domain.TournamentPresetV1 || record.State == domain.TournamentStateDraft ||
		!record.State.IsValid() || !record.Group.IsValid() {
		return domain.ErrInternal
	}
	return nil
}

func validatePublicTournamentSizes(record PublicTournamentRecord) error {
	if record.PlannedRosterSize < 4 || record.PlannedRosterSize > domain.TournamentMaxParticipants ||
		record.RosterSize < 0 || record.RosterSize > domain.TournamentMaxParticipants {
		return domain.ErrInternal
	}
	return nil
}

func validatePublicTournamentTimes(record PublicTournamentRecord) error {
	if !domain.IsValidServerTime(record.CreatedAt) || !validOptionalTime(record.StartedAt) ||
		!validOptionalTime(record.FinishedAt) || !validOptionalTime(record.ScheduledAt) {
		return domain.ErrInternal
	}
	return nil
}

func publicStateMatchesGroup(state domain.TournamentState, group inbound.PublicTournamentCatalogGroup) bool {
	switch group {
	case inbound.PublicTournamentCatalogGroupLive:
		return state == domain.TournamentStateSwiss || state == domain.TournamentStateGolden ||
			state == domain.TournamentStatePlayoffs || state == domain.TournamentStateTechnicalPause
	case inbound.PublicTournamentCatalogGroupUpcoming:
		return state == domain.TournamentStateRegistration || state == domain.TournamentStateRosterLocked
	case inbound.PublicTournamentCatalogGroupCompleted:
		return state == domain.TournamentStateCompleted || state == domain.TournamentStateCancelled
	default:
		return false
	}
}

func publicStageMatchesState(state, stage domain.TournamentState) bool {
	if state == domain.TournamentStateTechnicalPause {
		return stage == domain.TournamentStateSwiss || stage == domain.TournamentStateGolden || stage == domain.TournamentStatePlayoffs
	}
	return stage == state && stage != domain.TournamentStateDraft && stage != domain.TournamentStateTechnicalPause
}

func groupRank(group inbound.PublicTournamentCatalogGroup) int {
	switch group {
	case inbound.PublicTournamentCatalogGroupLive:
		return 0
	case inbound.PublicTournamentCatalogGroupUpcoming:
		return 1
	case inbound.PublicTournamentCatalogGroupCompleted:
		return 2
	default:
		return -1
	}
}

func validOptionalTime(value *time.Time) bool {
	return value == nil || domain.IsValidServerTime(*value)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
