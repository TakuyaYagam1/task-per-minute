package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type CategoryMode string

const (
	CategoryModeRandom CategoryMode = "random"
	CategoryModeAdmin  CategoryMode = "admin"
	CategoryModeDraft  CategoryMode = "draft"
)

type DraftState string

const (
	DraftStateActive    DraftState = "active"
	DraftStateCompleted DraftState = "completed"
)

type DraftActionType string

const (
	DraftActionBan  DraftActionType = "ban"
	DraftActionPick DraftActionType = "pick"
)

var (
	ErrInvalidDraft       = errors.New("invalid draft")
	ErrDraftCompleted     = errors.New("draft completed")
	ErrDraftStaleTurn     = errors.New("stale draft turn")
	ErrDraftIllegalAction = errors.New("illegal draft action")
	ErrDraftCategoryUsed  = errors.New("draft category already used")
	ErrDraftDeadline      = errors.New("draft deadline passed")
)

type DraftTurn struct {
	Number   int
	ActorID  uuid.UUID
	Action   DraftActionType
	Deadline time.Time
}

type DraftAction struct {
	Turn         int
	ActorID      uuid.UUID
	Action       DraftActionType
	Category     Category
	OccurredAt   time.Time
	TurnDeadline time.Time
}

type Draft struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	Format              SeriesFormat
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Pool                []Category
	State               DraftState
	Turn                int
	TurnDeadline        time.Time
	Actions             []DraftAction
	SelectedCategories  []Category
}

type draftTurnRule struct {
	actorOffset int
	action      DraftActionType
}

func (m CategoryMode) IsValid() bool {
	return m == CategoryModeRandom || m == CategoryModeAdmin || m == CategoryModeDraft
}

func (s DraftState) IsValid() bool {
	return s == DraftStateActive || s == DraftStateCompleted
}

func (a DraftActionType) IsValid() bool {
	return a == DraftActionBan || a == DraftActionPick
}

func NewDraft(
	id uuid.UUID,
	seriesID uuid.UUID,
	format SeriesFormat,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	pool []Category,
	firstDeadline time.Time,
) (Draft, error) {
	draft := Draft{
		ID:                  id,
		SeriesID:            seriesID,
		Format:              format,
		FirstParticipantID:  firstParticipantID,
		SecondParticipantID: secondParticipantID,
		Pool:                append([]Category(nil), pool...),
		State:               DraftStateActive,
		Turn:                1,
		TurnDeadline:        firstDeadline,
	}
	if err := draft.Validate(); err != nil {
		return Draft{}, err
	}
	return draft, nil
}

func (d Draft) Validate() error {
	if err := d.validateIdentity(); err != nil {
		return err
	}
	rules, err := d.validateFormatAndPool()
	if err != nil {
		return err
	}
	if !d.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidDraft, d.State)
	}
	if len(d.Actions) > len(rules) {
		return fmt.Errorf("%w: too many actions", ErrInvalidDraft)
	}
	if err := d.validateActions(rules); err != nil {
		return err
	}
	return d.validateLifecycleEvidence(rules)
}

func (d Draft) validateIdentity() error {
	if d.ID == uuid.Nil || d.SeriesID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidDraft)
	}
	if d.FirstParticipantID == uuid.Nil || d.SecondParticipantID == uuid.Nil || d.FirstParticipantID == d.SecondParticipantID {
		return fmt.Errorf("%w: invalid participants", ErrInvalidDraft)
	}
	return nil
}

func (d Draft) validateFormatAndPool() ([]draftTurnRule, error) {
	rules, poolSize, err := draftRules(d.Format)
	if err != nil {
		return nil, err
	}
	if len(d.Pool) != poolSize {
		return nil, fmt.Errorf("%w: %s requires %d categories", ErrInvalidDraft, d.Format, poolSize)
	}
	if err := validateDraftPool(d.Pool); err != nil {
		return nil, err
	}
	return rules, nil
}

func (d Draft) validateLifecycleEvidence(rules []draftTurnRule) error {
	if d.State == DraftStateActive {
		return d.validateActiveEvidence(rules)
	}
	if len(d.Actions) != len(rules) || d.Turn != len(rules) || !d.TurnDeadline.IsZero() {
		return fmt.Errorf("%w: invalid completed turn evidence", ErrInvalidDraft)
	}
	expected := d.deriveSelectedCategories()
	if !equalCategories(d.SelectedCategories, expected) {
		return fmt.Errorf("%w: selected categories do not match actions", ErrInvalidDraft)
	}
	return nil
}

func (d Draft) validateActiveEvidence(rules []draftTurnRule) error {
	if d.Turn != len(d.Actions)+1 || d.Turn > len(rules) || d.TurnDeadline.IsZero() || len(d.SelectedCategories) != 0 {
		return fmt.Errorf("%w: invalid active turn evidence", ErrInvalidDraft)
	}
	return nil
}

func (d Draft) CurrentTurn() (DraftTurn, error) {
	if d.State == DraftStateCompleted {
		return DraftTurn{}, ErrDraftCompleted
	}
	rules, _, err := draftRules(d.Format)
	if err != nil {
		return DraftTurn{}, err
	}
	if d.Turn < 1 || d.Turn > len(rules) {
		return DraftTurn{}, fmt.Errorf("%w: invalid current turn", ErrInvalidDraft)
	}
	rule := rules[d.Turn-1]
	return DraftTurn{Number: d.Turn, ActorID: d.actorID(rule.actorOffset), Action: rule.action, Deadline: d.TurnDeadline}, nil
}

func (d *Draft) ApplyAction(
	expectedTurn int,
	actorID uuid.UUID,
	action DraftActionType,
	category Category,
	at time.Time,
	nextDeadline time.Time,
) error {
	if d == nil {
		return fmt.Errorf("%w: nil draft", ErrInvalidDraft)
	}
	if err := d.Validate(); err != nil {
		return err
	}
	if d.State == DraftStateCompleted {
		return ErrDraftCompleted
	}
	turn, isLast, err := d.validateActionRequest(expectedTurn, actorID, action, category, at, nextDeadline)
	if err != nil {
		return err
	}
	d.Actions = append(d.Actions, DraftAction{
		Turn:         d.Turn,
		ActorID:      actorID,
		Action:       action,
		Category:     category,
		OccurredAt:   at,
		TurnDeadline: turn.Deadline,
	})
	if isLast {
		d.State = DraftStateCompleted
		d.TurnDeadline = time.Time{}
		d.SelectedCategories = d.deriveSelectedCategories()
		return nil
	}
	d.Turn++
	d.TurnDeadline = nextDeadline
	return nil
}

func (d Draft) validateActionRequest(
	expectedTurn int,
	actorID uuid.UUID,
	action DraftActionType,
	category Category,
	at time.Time,
	nextDeadline time.Time,
) (DraftTurn, bool, error) {
	if expectedTurn != d.Turn {
		return DraftTurn{}, false, ErrDraftStaleTurn
	}
	turn, err := d.CurrentTurn()
	if err != nil {
		return DraftTurn{}, false, err
	}
	if actorID != turn.ActorID || action != turn.Action {
		return DraftTurn{}, false, ErrDraftIllegalAction
	}
	if at.After(turn.Deadline) {
		return DraftTurn{}, false, ErrDraftDeadline
	}
	if !category.IsValid() || !containsCategory(d.Pool, category) {
		return DraftTurn{}, false, fmt.Errorf("%w: category %q is outside pool", ErrDraftIllegalAction, category)
	}
	if d.categoryUsed(category) {
		return DraftTurn{}, false, ErrDraftCategoryUsed
	}
	rules, _, _ := draftRules(d.Format)
	isLast := d.Turn == len(rules)
	if !isLast && (nextDeadline.IsZero() || !nextDeadline.After(at)) {
		return DraftTurn{}, false, fmt.Errorf("%w: next deadline must advance", ErrInvalidDraft)
	}
	if isLast && !nextDeadline.IsZero() {
		return DraftTurn{}, false, fmt.Errorf("%w: completed draft cannot have next deadline", ErrInvalidDraft)
	}
	return turn, isLast, nil
}

func (d Draft) validateActions(rules []draftTurnRule) error {
	used := make(map[Category]struct{}, len(d.Actions))
	for i, action := range d.Actions {
		rule := rules[i]
		if action.Turn != i+1 || action.ActorID != d.actorID(rule.actorOffset) || action.Action != rule.action {
			return fmt.Errorf("%w: action %d violates turn plan", ErrInvalidDraft, i+1)
		}
		if !containsCategory(d.Pool, action.Category) || !action.Category.IsValid() {
			return fmt.Errorf("%w: action category is outside pool", ErrInvalidDraft)
		}
		if _, exists := used[action.Category]; exists {
			return fmt.Errorf("%w: duplicate action category", ErrInvalidDraft)
		}
		if action.OccurredAt.IsZero() || action.TurnDeadline.IsZero() || action.OccurredAt.After(action.TurnDeadline) {
			return fmt.Errorf("%w: invalid action deadline evidence", ErrInvalidDraft)
		}
		used[action.Category] = struct{}{}
	}
	return nil
}

func (d Draft) deriveSelectedCategories() []Category {
	selected := make([]Category, 0, d.Format.WinsRequired()*2-1)
	used := make(map[Category]struct{}, len(d.Actions))
	for _, action := range d.Actions {
		used[action.Category] = struct{}{}
		if action.Action == DraftActionPick {
			selected = append(selected, action.Category)
		}
	}
	for _, category := range d.Pool {
		if _, exists := used[category]; !exists {
			selected = append(selected, category)
		}
	}
	return selected
}

func (d Draft) categoryUsed(category Category) bool {
	for _, action := range d.Actions {
		if action.Category == category {
			return true
		}
	}
	return false
}

func (d Draft) actorID(offset int) uuid.UUID {
	if offset == 0 {
		return d.FirstParticipantID
	}
	return d.SecondParticipantID
}

func draftRules(format SeriesFormat) ([]draftTurnRule, int, error) {
	switch format {
	case SeriesFormatBO1:
		return []draftTurnRule{
			{actorOffset: 0, action: DraftActionBan},
			{actorOffset: 1, action: DraftActionBan},
		}, 3, nil
	case SeriesFormatBO3:
		return []draftTurnRule{
			{actorOffset: 0, action: DraftActionBan},
			{actorOffset: 1, action: DraftActionBan},
			{actorOffset: 0, action: DraftActionPick},
			{actorOffset: 1, action: DraftActionPick},
		}, 5, nil
	default:
		return nil, 0, fmt.Errorf("%w: unknown format %q", ErrInvalidDraft, format)
	}
}

func validateDraftPool(pool []Category) error {
	seen := make(map[Category]struct{}, len(pool))
	for _, category := range pool {
		if !category.IsValid() {
			return fmt.Errorf("%w: invalid category %q", ErrInvalidDraft, category)
		}
		if _, exists := seen[category]; exists {
			return fmt.Errorf("%w: duplicate category %q", ErrInvalidDraft, category)
		}
		seen[category] = struct{}{}
	}
	return nil
}

func containsCategory(categories []Category, target Category) bool {
	for _, category := range categories {
		if category == target {
			return true
		}
	}
	return false
}

func equalCategories(first, second []Category) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i] != second[i] {
			return false
		}
	}
	return true
}
