package domain

import (
	"errors"
	"time"
)

type ArenaPreset string

const (
	ArenaPresetV1 ArenaPreset = "arena_v1"

	ArenaMinParticipants = 4
	ArenaMaxParticipants = 16
	ArenaTaskDuration    = 180 * time.Second
	ArenaNominalDuration = 60 * time.Minute
)

var ErrInvalidArenaRosterSize = errors.New("invalid Arena roster size")

func (p ArenaPreset) IsValid() bool {
	return p == ArenaPresetV1
}

func (p ArenaPreset) String() string {
	return string(p)
}

func (p ArenaPreset) MinParticipants() int {
	if p != ArenaPresetV1 {
		return 0
	}
	return ArenaMinParticipants
}

func (p ArenaPreset) MaxParticipants() int {
	if p != ArenaPresetV1 {
		return 0
	}
	return ArenaMaxParticipants
}

func (p ArenaPreset) TaskDuration() time.Duration {
	if p != ArenaPresetV1 {
		return 0
	}
	return ArenaTaskDuration
}

func (p ArenaPreset) NominalDuration() time.Duration {
	if p != ArenaPresetV1 {
		return 0
	}
	return ArenaNominalDuration
}

func (p ArenaPreset) EnforcesNominalDuration() bool {
	return false
}

func (p ArenaPreset) ValidRosterSize(participants int) bool {
	return p == ArenaPresetV1 &&
		participants >= ArenaMinParticipants &&
		participants <= ArenaMaxParticipants
}

func (p ArenaPreset) SwissRounds(participants int) (int, error) {
	if !p.ValidRosterSize(participants) {
		return 0, ErrInvalidArenaRosterSize
	}
	if participants <= 8 {
		return 3, nil
	}
	return 4, nil
}
