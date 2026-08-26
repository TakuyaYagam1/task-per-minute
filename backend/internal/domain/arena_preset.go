package domain

import (
	"errors"
	"time"
)

type ArenaPreset string

const (
	ArenaPreset60V1 ArenaPreset = "arena_60_v1"

	Arena60MinParticipants = 4
	Arena60MaxParticipants = 16
	Arena60TaskDuration    = 180 * time.Second
	Arena60NominalDuration = 60 * time.Minute
)

var ErrInvalidArenaRosterSize = errors.New("invalid Arena roster size")

func (p ArenaPreset) IsValid() bool {
	return p == ArenaPreset60V1
}

func (p ArenaPreset) String() string {
	return string(p)
}

func (p ArenaPreset) MinParticipants() int {
	if p != ArenaPreset60V1 {
		return 0
	}
	return Arena60MinParticipants
}

func (p ArenaPreset) MaxParticipants() int {
	if p != ArenaPreset60V1 {
		return 0
	}
	return Arena60MaxParticipants
}

func (p ArenaPreset) TaskDuration() time.Duration {
	if p != ArenaPreset60V1 {
		return 0
	}
	return Arena60TaskDuration
}

func (p ArenaPreset) NominalDuration() time.Duration {
	if p != ArenaPreset60V1 {
		return 0
	}
	return Arena60NominalDuration
}

func (p ArenaPreset) EnforcesNominalDuration() bool {
	return false
}

func (p ArenaPreset) ValidRosterSize(participants int) bool {
	return p == ArenaPreset60V1 &&
		participants >= Arena60MinParticipants &&
		participants <= Arena60MaxParticipants
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
