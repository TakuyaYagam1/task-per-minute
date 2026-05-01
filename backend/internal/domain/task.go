package domain

import (
	"math"
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

const TaskTitleMaxRunes = 255

func IsValidTaskTitle(title string) bool {
	n := utf8.RuneCountInString(title)
	return n > 0 && n <= TaskTitleMaxRunes
}

func IsValidTaskTimeLimit(seconds int) bool {
	return seconds > 0 && seconds <= math.MaxInt32
}

func IsValidTaskFlag(flag string) bool {
	return flag != ""
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
