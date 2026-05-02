package domain

import (
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

func (d Difficulty) IsValid() bool {
	switch d {
	case DifficultyEasy, DifficultyMedium, DifficultyHard:
		return true
	}
	return false
}

func (d Difficulty) String() string {
	return string(d)
}

type Category string

const (
	CategoryWeb       Category = "web"
	CategoryCrypto    Category = "crypto"
	CategoryForensics Category = "forensics"
	CategoryReverse   Category = "reverse"
	CategoryPwn       Category = "pwn"
	CategoryMisc      Category = "misc"
)

func (c Category) IsValid() bool {
	switch c {
	case CategoryWeb, CategoryCrypto, CategoryForensics, CategoryReverse, CategoryPwn, CategoryMisc:
		return true
	}
	return false
}

func (c Category) String() string {
	return string(c)
}

const (
	TaskTitleMaxRunes = 255
	TaskFlagMaxRunes  = 255
)

func IsValidTaskTitle(title string) bool {
	n := utf8.RuneCountInString(title)
	return n > 0 && n <= TaskTitleMaxRunes
}

func IsValidTaskDescription(description string) bool {
	return strings.TrimSpace(description) != ""
}

func IsValidTaskTimeLimit(seconds int) bool {
	return seconds > 0 && seconds <= math.MaxInt32
}

func IsValidTaskFlag(flag string) bool {
	n := utf8.RuneCountInString(flag)
	return n > 0 && n <= TaskFlagMaxRunes
}

func IsValidOptionalTaskURL(raw *string) bool {
	if raw == nil {
		return true
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return false
	}
	if isValidHostPortTaskURL(value) {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func isValidHostPortTaskURL(value string) bool {
	if strings.Contains(value, "://") {
		return false
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || strings.TrimSpace(host) == "" {
		return false
	}
	portNumber, err := strconv.Atoi(port)
	return err == nil && portNumber > 0 && portNumber <= 65535
}

type Task struct {
	ID            uuid.UUID
	Title         string
	Description   string
	Category      Category
	Difficulty    Difficulty
	TimeLimit     int
	Flag          string
	Hints         []string
	TaskURL       *string
	SourceFileURL *string
	CreatedAt     time.Time
}
