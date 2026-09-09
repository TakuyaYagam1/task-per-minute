package domain

import (
	"errors"
	"time"
)

type TournamentPreset string

const (
	TournamentPresetV1 TournamentPreset = "tournament_v1"

	TournamentMinParticipants = 4
	TournamentMaxParticipants = 16
	TournamentTaskDuration    = 180 * time.Second
	TournamentNominalDuration = 60 * time.Minute
)

var ErrInvalidRosterSize = errors.New("invalid tournament roster size")

func (p TournamentPreset) IsValid() bool {
	return p == TournamentPresetV1
}

func (p TournamentPreset) String() string {
	return string(p)
}

func (p TournamentPreset) MinParticipants() int {
	if p != TournamentPresetV1 {
		return 0
	}
	return TournamentMinParticipants
}

func (p TournamentPreset) MaxParticipants() int {
	if p != TournamentPresetV1 {
		return 0
	}
	return TournamentMaxParticipants
}

func (p TournamentPreset) TaskDuration() time.Duration {
	if p != TournamentPresetV1 {
		return 0
	}
	return TournamentTaskDuration
}

func (p TournamentPreset) NominalDuration() time.Duration {
	if p != TournamentPresetV1 {
		return 0
	}
	return TournamentNominalDuration
}

func (p TournamentPreset) EnforcesNominalDuration() bool {
	return false
}

func (p TournamentPreset) ValidRosterSize(participants int) bool {
	return p == TournamentPresetV1 &&
		participants >= TournamentMinParticipants &&
		participants <= TournamentMaxParticipants
}

func (p TournamentPreset) SwissRounds(participants int) (int, error) {
	if !p.ValidRosterSize(participants) {
		return 0, ErrInvalidRosterSize
	}
	if participants <= 8 {
		return 3, nil
	}
	return 4, nil
}
