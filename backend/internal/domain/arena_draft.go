package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ArenaCategoryMode string

const (
	ArenaCategoryModeRandom ArenaCategoryMode = "random"
	ArenaCategoryModeAdmin  ArenaCategoryMode = "admin"
	ArenaCategoryModeDraft  ArenaCategoryMode = "draft"
)

type ArenaDraftState string

const (
	ArenaDraftStateActive    ArenaDraftState = "active"
	ArenaDraftStateCompleted ArenaDraftState = "completed"
)

type ArenaDraftActionType string

const (
	ArenaDraftActionBan  ArenaDraftActionType = "ban"
	ArenaDraftActionPick ArenaDraftActionType = "pick"
)

var (
	ErrInvalidArenaDraft       = errors.New("invalid arena draft")
	ErrArenaDraftCompleted     = errors.New("arena draft completed")
	ErrArenaDraftStaleTurn     = errors.New("stale arena draft turn")
	ErrArenaDraftIllegalAction = errors.New("illegal arena draft action")
	ErrArenaDraftCategoryUsed  = errors.New("arena draft category already used")
	ErrArenaDraftDeadline      = errors.New("arena draft deadline passed")
)

type ArenaDraftTurn struct {
	Number   int
	ActorID  uuid.UUID
	Action   ArenaDraftActionType
	Deadline time.Time
}

type ArenaDraftAction struct {
	Turn         int
	ActorID      uuid.UUID
	Action       ArenaDraftActionType
	Category     Category
	OccurredAt   time.Time
	TurnDeadline time.Time
}

type ArenaDraft struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	Format              ArenaSeriesFormat
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Pool                []Category
	State               ArenaDraftState
	Turn                int
	TurnDeadline        time.Time
	Actions             []ArenaDraftAction
	SelectedCategories  []Category
}

type arenaDraftTurnRule struct {
	actorOffset int
	action      ArenaDraftActionType
}

func (m ArenaCategoryMode) IsValid() bool {
	return m == ArenaCategoryModeRandom || m == ArenaCategoryModeAdmin || m == ArenaCategoryModeDraft
}

func (s ArenaDraftState) IsValid() bool {
	return s == ArenaDraftStateActive || s == ArenaDraftStateCompleted
}

func (a ArenaDraftActionType) IsValid() bool {
	return a == ArenaDraftActionBan || a == ArenaDraftActionPick
}

func NewArenaDraft(
	id uuid.UUID,
	seriesID uuid.UUID,
	format ArenaSeriesFormat,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	pool []Category,
	firstDeadline time.Time,
) (ArenaDraft, error) {
	draft := ArenaDraft{
		ID:                  id,
		SeriesID:            seriesID,
		Format:              format,
		FirstParticipantID:  firstParticipantID,
		SecondParticipantID: secondParticipantID,
		Pool:                append([]Category(nil), pool...),
		State:               ArenaDraftStateActive,
		Turn:                1,
		TurnDeadline:        firstDeadline,
	}
	if err := draft.Validate(); err != nil {
		return ArenaDraft{}, err
	}
	return draft, nil
}

func (d ArenaDraft) Validate() error {
	if err := d.validateIdentity(); err != nil {
		return err
	}
	rules, err := d.validateFormatAndPool()
	if err != nil {
		return err
	}
	if !d.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidArenaDraft, d.State)
	}
	if len(d.Actions) > len(rules) {
		return fmt.Errorf("%w: too many actions", ErrInvalidArenaDraft)
	}
	if err := d.validateActions(rules); err != nil {
		return err
	}
	return d.validateLifecycleEvidence(rules)
}

func (d ArenaDraft) validateIdentity() error {
	if d.ID == uuid.Nil || d.SeriesID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidArenaDraft)
	}
	if d.FirstParticipantID == uuid.Nil || d.SecondParticipantID == uuid.Nil || d.FirstParticipantID == d.SecondParticipantID {
		return fmt.Errorf("%w: invalid participants", ErrInvalidArenaDraft)
	}
	return nil
}

func (d ArenaDraft) validateFormatAndPool() ([]arenaDraftTurnRule, error) {
	rules, poolSize, err := arenaDraftRules(d.Format)
	if err != nil {
		return nil, err
	}
	if len(d.Pool) != poolSize {
		return nil, fmt.Errorf("%w: %s requires %d categories", ErrInvalidArenaDraft, d.Format, poolSize)
	}
	if err := validateArenaDraftPool(d.Pool); err != nil {
		return nil, err
	}
	return rules, nil
}

func (d ArenaDraft) validateLifecycleEvidence(rules []arenaDraftTurnRule) error {
	if d.State == ArenaDraftStateActive {
		return d.validateActiveEvidence(rules)
	}
	if len(d.Actions) != len(rules) || d.Turn != len(rules) || !d.TurnDeadline.IsZero() {
		return fmt.Errorf("%w: invalid completed turn evidence", ErrInvalidArenaDraft)
	}
	expected := d.deriveSelectedCategories()
	if !equalCategories(d.SelectedCategories, expected) {
		return fmt.Errorf("%w: selected categories do not match actions", ErrInvalidArenaDraft)
	}
	return nil
}

func (d ArenaDraft) validateActiveEvidence(rules []arenaDraftTurnRule) error {
	if d.Turn != len(d.Actions)+1 || d.Turn > len(rules) || d.TurnDeadline.IsZero() || len(d.SelectedCategories) != 0 {
		return fmt.Errorf("%w: invalid active turn evidence", ErrInvalidArenaDraft)
	}
	return nil
}

func (d ArenaDraft) CurrentTurn() (ArenaDraftTurn, error) {
	if d.State == ArenaDraftStateCompleted {
		return ArenaDraftTurn{}, ErrArenaDraftCompleted
	}
	rules, _, err := arenaDraftRules(d.Format)
	if err != nil {
		return ArenaDraftTurn{}, err
	}
	if d.Turn < 1 || d.Turn > len(rules) {
		return ArenaDraftTurn{}, fmt.Errorf("%w: invalid current turn", ErrInvalidArenaDraft)
	}
	rule := rules[d.Turn-1]
	return ArenaDraftTurn{Number: d.Turn, ActorID: d.actorID(rule.actorOffset), Action: rule.action, Deadline: d.TurnDeadline}, nil
}

func (d *ArenaDraft) ApplyAction(
	expectedTurn int,
	actorID uuid.UUID,
	action ArenaDraftActionType,
	category Category,
	at time.Time,
	nextDeadline time.Time,
) error {
	if d == nil {
		return fmt.Errorf("%w: nil draft", ErrInvalidArenaDraft)
	}
	if err := d.Validate(); err != nil {
		return err
	}
	if d.State == ArenaDraftStateCompleted {
		return ErrArenaDraftCompleted
	}
	turn, isLast, err := d.validateActionRequest(expectedTurn, actorID, action, category, at, nextDeadline)
	if err != nil {
		return err
	}
	d.Actions = append(d.Actions, ArenaDraftAction{
		Turn:         d.Turn,
		ActorID:      actorID,
		Action:       action,
		Category:     category,
		OccurredAt:   at,
		TurnDeadline: turn.Deadline,
	})
	if isLast {
		d.State = ArenaDraftStateCompleted
		d.TurnDeadline = time.Time{}
		d.SelectedCategories = d.deriveSelectedCategories()
		return nil
	}
	d.Turn++
	d.TurnDeadline = nextDeadline
	return nil
}

func (d ArenaDraft) validateActionRequest(
	expectedTurn int,
	actorID uuid.UUID,
	action ArenaDraftActionType,
	category Category,
	at time.Time,
	nextDeadline time.Time,
) (ArenaDraftTurn, bool, error) {
	if expectedTurn != d.Turn {
		return ArenaDraftTurn{}, false, ErrArenaDraftStaleTurn
	}
	turn, err := d.CurrentTurn()
	if err != nil {
		return ArenaDraftTurn{}, false, err
	}
	if actorID != turn.ActorID || action != turn.Action {
		return ArenaDraftTurn{}, false, ErrArenaDraftIllegalAction
	}
	if at.After(turn.Deadline) {
		return ArenaDraftTurn{}, false, ErrArenaDraftDeadline
	}
	if !category.IsValid() || !containsCategory(d.Pool, category) {
		return ArenaDraftTurn{}, false, fmt.Errorf("%w: category %q is outside pool", ErrArenaDraftIllegalAction, category)
	}
	if d.categoryUsed(category) {
		return ArenaDraftTurn{}, false, ErrArenaDraftCategoryUsed
	}
	rules, _, _ := arenaDraftRules(d.Format)
	isLast := d.Turn == len(rules)
	if !isLast && (nextDeadline.IsZero() || !nextDeadline.After(at)) {
		return ArenaDraftTurn{}, false, fmt.Errorf("%w: next deadline must advance", ErrInvalidArenaDraft)
	}
	if isLast && !nextDeadline.IsZero() {
		return ArenaDraftTurn{}, false, fmt.Errorf("%w: completed draft cannot have next deadline", ErrInvalidArenaDraft)
	}
	return turn, isLast, nil
}

func (d ArenaDraft) validateActions(rules []arenaDraftTurnRule) error {
	used := make(map[Category]struct{}, len(d.Actions))
	for i, action := range d.Actions {
		rule := rules[i]
		if action.Turn != i+1 || action.ActorID != d.actorID(rule.actorOffset) || action.Action != rule.action {
			return fmt.Errorf("%w: action %d violates turn plan", ErrInvalidArenaDraft, i+1)
		}
		if !containsCategory(d.Pool, action.Category) || !action.Category.IsValid() {
			return fmt.Errorf("%w: action category is outside pool", ErrInvalidArenaDraft)
		}
		if _, exists := used[action.Category]; exists {
			return fmt.Errorf("%w: duplicate action category", ErrInvalidArenaDraft)
		}
		if action.OccurredAt.IsZero() || action.TurnDeadline.IsZero() || action.OccurredAt.After(action.TurnDeadline) {
			return fmt.Errorf("%w: invalid action deadline evidence", ErrInvalidArenaDraft)
		}
		used[action.Category] = struct{}{}
	}
	return nil
}

func (d ArenaDraft) deriveSelectedCategories() []Category {
	selected := make([]Category, 0, d.Format.WinsRequired()*2-1)
	used := make(map[Category]struct{}, len(d.Actions))
	for _, action := range d.Actions {
		used[action.Category] = struct{}{}
		if action.Action == ArenaDraftActionPick {
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

func (d ArenaDraft) categoryUsed(category Category) bool {
	for _, action := range d.Actions {
		if action.Category == category {
			return true
		}
	}
	return false
}

func (d ArenaDraft) actorID(offset int) uuid.UUID {
	if offset == 0 {
		return d.FirstParticipantID
	}
	return d.SecondParticipantID
}

func arenaDraftRules(format ArenaSeriesFormat) ([]arenaDraftTurnRule, int, error) {
	switch format {
	case ArenaSeriesFormatBO1:
		return []arenaDraftTurnRule{
			{actorOffset: 0, action: ArenaDraftActionBan},
			{actorOffset: 1, action: ArenaDraftActionBan},
		}, 3, nil
	case ArenaSeriesFormatBO3:
		return []arenaDraftTurnRule{
			{actorOffset: 0, action: ArenaDraftActionBan},
			{actorOffset: 1, action: ArenaDraftActionBan},
			{actorOffset: 0, action: ArenaDraftActionPick},
			{actorOffset: 1, action: ArenaDraftActionPick},
		}, 5, nil
	default:
		return nil, 0, fmt.Errorf("%w: unknown format %q", ErrInvalidArenaDraft, format)
	}
}

func validateArenaDraftPool(pool []Category) error {
	seen := make(map[Category]struct{}, len(pool))
	for _, category := range pool {
		if !category.IsValid() {
			return fmt.Errorf("%w: invalid category %q", ErrInvalidArenaDraft, category)
		}
		if _, exists := seen[category]; exists {
			return fmt.Errorf("%w: duplicate category %q", ErrInvalidArenaDraft, category)
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
