package domain

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	TournamentNameMaxLength     = 120
	TournamentPublicIDMaxLength = 64
)

var ErrInvalidTournamentMetadata = errors.New("invalid tournament metadata")

type TournamentMetadata struct {
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
}

func (m TournamentMetadata) Validate(preset TournamentPreset) error {
	if !validTournamentName(m.Name) || !validTournamentPublicID(m.PublicID) ||
		!preset.ValidRosterSize(m.PlannedRosterSize) || m.ContentRevision < 1 {
		return ErrInvalidTournamentMetadata
	}
	return nil
}

func validTournamentName(value string) bool {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || value == "" ||
		utf8.RuneCountInString(value) > TournamentNameMaxLength {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validTournamentPublicID(value string) bool {
	if value == "" || len(value) > TournamentPublicIDMaxLength || value != strings.ToLower(value) ||
		value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, char := range value {
		if char == '-' {
			if previousHyphen {
				return false
			}
			previousHyphen = true
			continue
		}
		if char < 'a' || char > 'z' {
			if char < '0' || char > '9' {
				return false
			}
		}
		previousHyphen = false
	}
	return true
}
