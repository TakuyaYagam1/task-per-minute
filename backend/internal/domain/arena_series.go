package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type ArenaSeriesFormat string

const (
	ArenaSeriesFormatBO1 ArenaSeriesFormat = "bo1"
	ArenaSeriesFormatBO3 ArenaSeriesFormat = "bo3"
)

type ArenaSeriesState string

const (
	ArenaSeriesStatePlanned        ArenaSeriesState = "planned"
	ArenaSeriesStateLocked         ArenaSeriesState = "locked"
	ArenaSeriesStateDraft          ArenaSeriesState = "draft"
	ArenaSeriesStateReady          ArenaSeriesState = "ready"
	ArenaSeriesStateActive         ArenaSeriesState = "active"
	ArenaSeriesStateReplayRequired ArenaSeriesState = "replay_required"
	ArenaSeriesStateTechnicalPause ArenaSeriesState = "technical_pause"
	ArenaSeriesStateCompleted      ArenaSeriesState = "completed"
	ArenaSeriesStateCancelled      ArenaSeriesState = "cancelled"
)

type ArenaOfficialResultRevisionID uuid.UUID
type ArenaSeriesScoreRevisionID uuid.UUID

var ErrInvalidArenaSeries = errors.New("invalid arena series")

type ArenaSeriesScore struct {
	FirstParticipantWins  int
	SecondParticipantWins int
}

type ArenaSeries struct {
	ID                      uuid.UUID
	TournamentID            uuid.UUID
	FirstParticipantID      uuid.UUID
	SecondParticipantID     uuid.UUID
	Format                  ArenaSeriesFormat
	State                   ArenaSeriesState
	Score                   ArenaSeriesScore
	WinnerID                *uuid.UUID
	Slots                   []ArenaGameSlot
	CurrentScoreRevisionID  *ArenaSeriesScoreRevisionID
	CurrentResultRevisionID *ArenaOfficialResultRevisionID
}

func (f ArenaSeriesFormat) IsValid() bool {
	return f == ArenaSeriesFormatBO1 || f == ArenaSeriesFormatBO3
}

func (f ArenaSeriesFormat) WinsRequired() int {
	switch f {
	case ArenaSeriesFormatBO1:
		return 1
	case ArenaSeriesFormatBO3:
		return 2
	default:
		return 0
	}
}

func (s ArenaSeriesState) IsValid() bool {
	switch s {
	case ArenaSeriesStatePlanned,
		ArenaSeriesStateLocked,
		ArenaSeriesStateDraft,
		ArenaSeriesStateReady,
		ArenaSeriesStateActive,
		ArenaSeriesStateReplayRequired,
		ArenaSeriesStateTechnicalPause,
		ArenaSeriesStateCompleted,
		ArenaSeriesStateCancelled:
		return true
	}
	return false
}

func (s ArenaSeriesState) IsTerminal() bool {
	return s == ArenaSeriesStateCompleted || s == ArenaSeriesStateCancelled
}

func (id ArenaOfficialResultRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id ArenaOfficialResultRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (id ArenaSeriesScoreRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id ArenaSeriesScoreRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (s ArenaSeriesScore) Validate(format ArenaSeriesFormat) error {
	if !format.IsValid() {
		return fmt.Errorf("%w: unknown format %q", ErrInvalidArenaSeries, format)
	}
	if s.FirstParticipantWins < 0 || s.SecondParticipantWins < 0 {
		return fmt.Errorf("%w: negative score", ErrInvalidArenaSeries)
	}
	winsRequired := format.WinsRequired()
	if s.FirstParticipantWins > winsRequired || s.SecondParticipantWins > winsRequired {
		return fmt.Errorf("%w: score exceeds %s", ErrInvalidArenaSeries, format)
	}
	if s.FirstParticipantWins == winsRequired && s.SecondParticipantWins == winsRequired {
		return fmt.Errorf("%w: both participants cannot win", ErrInvalidArenaSeries)
	}
	return nil
}

func (s ArenaSeriesScore) Winner(firstID, secondID uuid.UUID, format ArenaSeriesFormat) *uuid.UUID {
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

func (s ArenaSeries) Validate() error {
	if err := s.validateIdentity(); err != nil {
		return err
	}
	if !s.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidArenaSeries, s.State)
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

func (s ArenaSeries) validateIdentity() error {
	if s.ID == uuid.Nil || s.TournamentID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidArenaSeries)
	}
	if s.FirstParticipantID == uuid.Nil || s.SecondParticipantID == uuid.Nil {
		return fmt.Errorf("%w: missing participant", ErrInvalidArenaSeries)
	}
	if s.FirstParticipantID == s.SecondParticipantID {
		return fmt.Errorf("%w: duplicate participant", ErrInvalidArenaSeries)
	}
	if !s.Format.IsValid() {
		return fmt.Errorf("%w: unknown format %q", ErrInvalidArenaSeries, s.Format)
	}
	return nil
}

func (s ArenaSeries) validateRevisionEvidence() error {
	if s.CurrentScoreRevisionID != nil && s.CurrentScoreRevisionID.IsZero() {
		return fmt.Errorf("%w: empty score revision identity", ErrInvalidArenaSeries)
	}
	if s.CurrentResultRevisionID != nil && s.CurrentResultRevisionID.IsZero() {
		return fmt.Errorf("%w: empty result revision identity", ErrInvalidArenaSeries)
	}
	if s.State.IsTerminal() {
		if s.CurrentScoreRevisionID == nil || s.CurrentResultRevisionID == nil {
			return fmt.Errorf("%w: terminal series requires current revisions", ErrInvalidArenaSeries)
		}
		return nil
	}
	if s.CurrentResultRevisionID != nil {
		return fmt.Errorf("%w: non-terminal series has result revision", ErrInvalidArenaSeries)
	}
	return nil
}

func (s ArenaSeries) validateWinner() error {
	if s.State == ArenaSeriesStateCompleted {
		expected := s.Score.Winner(s.FirstParticipantID, s.SecondParticipantID, s.Format)
		if expected == nil || s.WinnerID == nil || *expected != *s.WinnerID {
			return fmt.Errorf("%w: completed series requires score winner", ErrInvalidArenaSeries)
		}
		return nil
	}
	if s.WinnerID == nil {
		return nil
	}
	if !s.State.IsTerminal() {
		return fmt.Errorf("%w: non-terminal series has winner", ErrInvalidArenaSeries)
	}
	if *s.WinnerID != s.FirstParticipantID && *s.WinnerID != s.SecondParticipantID {
		return fmt.Errorf("%w: winner is not a participant", ErrInvalidArenaSeries)
	}
	return nil
}

func (s ArenaSeries) validateSlots() error {
	if len(s.Slots) > s.Format.WinsRequired()*2-1 {
		return fmt.Errorf("%w: too many game slots", ErrInvalidArenaSeries)
	}
	ids := make(map[uuid.UUID]struct{}, len(s.Slots))
	positions := make(map[int]struct{}, len(s.Slots))
	for _, slot := range s.Slots {
		if err := slot.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArenaSeries, err)
		}
		if err := slot.ScoreBefore.Validate(s.Format); err != nil {
			return fmt.Errorf("%w: slot score does not match series format", ErrInvalidArenaSeries)
		}
		if slot.SeriesID != s.ID {
			return fmt.Errorf("%w: slot belongs to another series", ErrInvalidArenaSeries)
		}
		if !s.slotWinnersBelongToSeries(slot) {
			return fmt.Errorf("%w: game winner is not a participant", ErrInvalidArenaSeries)
		}
		if _, exists := ids[slot.ID]; exists {
			return fmt.Errorf("%w: duplicate slot identity", ErrInvalidArenaSeries)
		}
		if _, exists := positions[slot.Position]; exists {
			return fmt.Errorf("%w: duplicate slot position", ErrInvalidArenaSeries)
		}
		ids[slot.ID] = struct{}{}
		positions[slot.Position] = struct{}{}
	}
	for position := 1; position <= len(s.Slots); position++ {
		if _, exists := positions[position]; !exists {
			return fmt.Errorf("%w: game slot positions are not contiguous", ErrInvalidArenaSeries)
		}
	}
	return nil
}

func (s ArenaSeries) slotWinnersBelongToSeries(slot ArenaGameSlot) bool {
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
