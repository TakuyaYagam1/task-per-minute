package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type SeriesFormat string

const (
	SeriesFormatBO1 SeriesFormat = "bo1"
	SeriesFormatBO3 SeriesFormat = "bo3"
)

type SeriesState string

const (
	SeriesStatePlanned        SeriesState = "planned"
	SeriesStateLocked         SeriesState = "locked"
	SeriesStateDraft          SeriesState = "draft"
	SeriesStateReady          SeriesState = "ready"
	SeriesStateActive         SeriesState = "active"
	SeriesStateReplayRequired SeriesState = "replay_required"
	SeriesStateTechnicalPause SeriesState = "technical_pause"
	SeriesStateCompleted      SeriesState = "completed"
	SeriesStateCancelled      SeriesState = "cancelled"
)

type OfficialResultRevisionID uuid.UUID
type SeriesScoreRevisionID uuid.UUID

var ErrInvalidSeries = errors.New("invalid series")

type SeriesScore struct {
	FirstParticipantWins  int
	SecondParticipantWins int
}

type Series struct {
	ID                      uuid.UUID
	TournamentID            uuid.UUID
	FirstParticipantID      uuid.UUID
	SecondParticipantID     uuid.UUID
	Format                  SeriesFormat
	State                   SeriesState
	Score                   SeriesScore
	WinnerID                *uuid.UUID
	Slots                   []GameSlot
	CurrentScoreRevisionID  *SeriesScoreRevisionID
	CurrentResultRevisionID *OfficialResultRevisionID
}

func (f SeriesFormat) IsValid() bool {
	return f == SeriesFormatBO1 || f == SeriesFormatBO3
}

func (f SeriesFormat) WinsRequired() int {
	switch f {
	case SeriesFormatBO1:
		return 1
	case SeriesFormatBO3:
		return 2
	default:
		return 0
	}
}

func (s SeriesState) IsValid() bool {
	switch s {
	case SeriesStatePlanned,
		SeriesStateLocked,
		SeriesStateDraft,
		SeriesStateReady,
		SeriesStateActive,
		SeriesStateReplayRequired,
		SeriesStateTechnicalPause,
		SeriesStateCompleted,
		SeriesStateCancelled:
		return true
	}
	return false
}

func (s SeriesState) IsTerminal() bool {
	return s == SeriesStateCompleted || s == SeriesStateCancelled
}

func (id OfficialResultRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id OfficialResultRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (id SeriesScoreRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id SeriesScoreRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (s SeriesScore) Validate(format SeriesFormat) error {
	if !format.IsValid() {
		return fmt.Errorf("%w: unknown format %q", ErrInvalidSeries, format)
	}
	if s.FirstParticipantWins < 0 || s.SecondParticipantWins < 0 {
		return fmt.Errorf("%w: negative score", ErrInvalidSeries)
	}
	winsRequired := format.WinsRequired()
	if s.FirstParticipantWins > winsRequired || s.SecondParticipantWins > winsRequired {
		return fmt.Errorf("%w: score exceeds %s", ErrInvalidSeries, format)
	}
	if s.FirstParticipantWins == winsRequired && s.SecondParticipantWins == winsRequired {
		return fmt.Errorf("%w: both participants cannot win", ErrInvalidSeries)
	}
	return nil
}

func (s SeriesScore) Winner(firstID, secondID uuid.UUID, format SeriesFormat) *uuid.UUID {
	winsRequired := format.WinsRequired()
	switch {
	case winsRequired == 0:
		return nil
	case s.FirstParticipantWins == winsRequired:
		winner := firstID
		return &winner
	case s.SecondParticipantWins == winsRequired:
		winner := secondID
		return &winner
	default:
		return nil
	}
}

func (s Series) Validate() error {
	if err := s.validateIdentity(); err != nil {
		return err
	}
	if !s.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidSeries, s.State)
	}
	if err := s.Score.Validate(s.Format); err != nil {
		return err
	}
	if err := s.validateRevisionEvidence(); err != nil {
		return err
	}
	if err := s.validateWinner(); err != nil {
		return err
	}
	return s.validateSlots()
}

func (s Series) validateIdentity() error {
	if s.ID == uuid.Nil || s.TournamentID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidSeries)
	}
	if s.FirstParticipantID == uuid.Nil || s.SecondParticipantID == uuid.Nil {
		return fmt.Errorf("%w: missing participant", ErrInvalidSeries)
	}
	if s.FirstParticipantID == s.SecondParticipantID {
		return fmt.Errorf("%w: duplicate participant", ErrInvalidSeries)
	}
	if !s.Format.IsValid() {
		return fmt.Errorf("%w: unknown format %q", ErrInvalidSeries, s.Format)
	}
	return nil
}

func (s Series) validateRevisionEvidence() error {
	if s.CurrentScoreRevisionID != nil && s.CurrentScoreRevisionID.IsZero() {
		return fmt.Errorf("%w: empty score revision identity", ErrInvalidSeries)
	}
	if s.CurrentResultRevisionID != nil && s.CurrentResultRevisionID.IsZero() {
		return fmt.Errorf("%w: empty result revision identity", ErrInvalidSeries)
	}
	if s.State.IsTerminal() {
		if s.CurrentScoreRevisionID == nil || s.CurrentResultRevisionID == nil {
			return fmt.Errorf("%w: terminal series requires current revisions", ErrInvalidSeries)
		}
		return nil
	}
	if s.CurrentResultRevisionID != nil {
		return fmt.Errorf("%w: non-terminal series has result revision", ErrInvalidSeries)
	}
	return nil
}

func (s Series) validateWinner() error {
	if s.State == SeriesStateCompleted {
		expected := s.Score.Winner(s.FirstParticipantID, s.SecondParticipantID, s.Format)
		if expected == nil || s.WinnerID == nil || *expected != *s.WinnerID {
			return fmt.Errorf("%w: completed series requires score winner", ErrInvalidSeries)
		}
		return nil
	}
	if s.WinnerID == nil {
		return nil
	}
	if !s.State.IsTerminal() {
		return fmt.Errorf("%w: non-terminal series has winner", ErrInvalidSeries)
	}
	if *s.WinnerID != s.FirstParticipantID && *s.WinnerID != s.SecondParticipantID {
		return fmt.Errorf("%w: winner is not a participant", ErrInvalidSeries)
	}
	return nil
}

func (s Series) validateSlots() error {
	if len(s.Slots) > s.Format.WinsRequired()*2-1 {
		return fmt.Errorf("%w: too many game slots", ErrInvalidSeries)
	}
	ids := make(map[uuid.UUID]struct{}, len(s.Slots))
	positions := make(map[int]struct{}, len(s.Slots))
	for _, slot := range s.Slots {
		if err := slot.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidSeries, err)
		}
		if err := slot.ScoreBefore.Validate(s.Format); err != nil {
			return fmt.Errorf("%w: slot score does not match series format", ErrInvalidSeries)
		}
		if slot.SeriesID != s.ID {
			return fmt.Errorf("%w: slot belongs to another series", ErrInvalidSeries)
		}
		if !s.slotWinnersBelongToSeries(slot) {
			return fmt.Errorf("%w: game winner is not a participant", ErrInvalidSeries)
		}
		if _, exists := ids[slot.ID]; exists {
			return fmt.Errorf("%w: duplicate slot identity", ErrInvalidSeries)
		}
		if _, exists := positions[slot.Position]; exists {
			return fmt.Errorf("%w: duplicate slot position", ErrInvalidSeries)
		}
		ids[slot.ID] = struct{}{}
		positions[slot.Position] = struct{}{}
	}
	for position := 1; position <= len(s.Slots); position++ {
		if _, exists := positions[position]; !exists {
			return fmt.Errorf("%w: game slot positions are not contiguous", ErrInvalidSeries)
		}
	}
	return nil
}

func (s Series) slotWinnersBelongToSeries(slot GameSlot) bool {
	for _, attempt := range slot.Attempts {
		if attempt.WinnerID == nil {
			continue
		}
		if *attempt.WinnerID != s.FirstParticipantID && *attempt.WinnerID != s.SecondParticipantID {
			return false
		}
	}
	return true
}
