package domain

import (
	"crypto/sha256"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	ErrorCodeTaskNotFound   ErrorCode = "task.not_found"
	ErrorCodeTaskInUse      ErrorCode = "task.in_use"
	ErrorCodeTaskValidation ErrorCode = "task.validation"
)

var (
	ErrTaskNotFound   = &Error{Code: ErrorCodeTaskNotFound, Message: "task not found"}
	ErrTaskInUse      = &Error{Code: ErrorCodeTaskInUse, Message: "task is in use by a tournament"}
	ErrTaskValidation = &Error{Code: ErrorCodeTaskValidation, Message: "task validation failed"}
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
	CategoryStego     Category = "steganography"
	CategoryPPC       Category = "ppc"
	CategoryOSINT     Category = "osint"
	CategoryMobile    Category = "mobile"
	CategoryHardware  Category = "hardware"
	CategoryMisc      Category = "misc"
)

func (c Category) IsValid() bool {
	switch c {
	case CategoryWeb,
		CategoryCrypto,
		CategoryForensics,
		CategoryReverse,
		CategoryPwn,
		CategoryStego,
		CategoryPPC,
		CategoryOSINT,
		CategoryMobile,
		CategoryHardware,
		CategoryMisc:
		return true
	}
	return false
}

func (c Category) String() string {
	return string(c)
}

type TaskKind string

const (
	TaskKindNormal TaskKind = "normal"
	TaskKindGolden TaskKind = "golden"
)

func (k TaskKind) IsValid() bool {
	return k == TaskKindNormal || k == TaskKindGolden
}

func (k TaskKind) String() string {
	return string(k)
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

func IsValidOptionalSourceFileURL(raw *string) bool {
	if raw == nil {
		return true
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func IsValidTaskURLShape(category Category, taskURL, sourceFileURL *string) bool {
	return category.IsValid() &&
		IsValidOptionalTaskURL(taskURL) &&
		IsValidOptionalSourceFileURL(sourceFileURL)
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
	ID             uuid.UUID
	Title          string
	Description    string
	Category       Category
	Difficulty     Difficulty
	TimeLimit      int
	Flag           string
	Hints          []string
	TaskURL        *string
	SourceFileURL  *string
	Kind           TaskKind
	Enabled        bool
	CurrentVersion int
	CreatedAt      time.Time
}

type TaskVersion struct {
	TaskID        uuid.UUID
	Version       int
	Title         string
	Description   string
	Category      Category
	Difficulty    Difficulty
	TimeLimit     int
	Flag          string
	Hints         []string
	TaskURL       *string
	SourceFileURL *string
	ContentDigest [sha256.Size]byte
	CreatedAt     time.Time
}

func (v TaskVersion) Validate() error {
	if v.TaskID == uuid.Nil || v.Version < 1 {
		return fmt.Errorf("%w: invalid task version identity", ErrTaskValidation)
	}
	if !IsValidTaskTitle(v.Title) ||
		!IsValidTaskDescription(v.Description) ||
		!v.Category.IsValid() ||
		!v.Difficulty.IsValid() ||
		!IsValidTaskTimeLimit(v.TimeLimit) ||
		!IsValidTaskFlag(v.Flag) ||
		!IsValidTaskHints(v.Hints) ||
		!IsValidTaskURLShape(v.Category, v.TaskURL, v.SourceFileURL) {
		return fmt.Errorf("%w: invalid task version content", ErrTaskValidation)
	}
	if v.ContentDigest == [sha256.Size]byte{} {
		return fmt.Errorf("%w: missing server content digest", ErrTaskValidation)
	}
	if v.CreatedAt.IsZero() || v.CreatedAt.Location() != time.UTC {
		return fmt.Errorf("%w: task version timestamp must be server UTC", ErrTaskValidation)
	}
	return nil
}

type TaskHealthAttestationSource string

const (
	TaskHealthAttestationSourceContentValidation TaskHealthAttestationSource = "content_validation"
	TaskHealthAttestationSourceProbe             TaskHealthAttestationSource = "probe"
)

func (s TaskHealthAttestationSource) IsValid() bool {
	return s == TaskHealthAttestationSourceContentValidation || s == TaskHealthAttestationSourceProbe
}

type TaskVersionHealthAttestation struct {
	TaskID     uuid.UUID
	Version    int
	Revision   int64
	Healthy    bool
	Source     TaskHealthAttestationSource
	AttestedAt time.Time
}

func (a TaskVersionHealthAttestation) Validate() error {
	if a.TaskID == uuid.Nil || a.Version < 1 || a.Revision < 1 || !a.Source.IsValid() {
		return fmt.Errorf("%w: invalid task health attestation identity", ErrTaskValidation)
	}
	if a.AttestedAt.IsZero() || a.AttestedAt.Location() != time.UTC {
		return fmt.Errorf("%w: task health attestation timestamp must be server UTC", ErrTaskValidation)
	}
	return nil
}

type TaskVersionHealthState struct {
	Attested bool
	Healthy  bool
	Revision int64
}

func CurrentTaskVersionHealth(
	taskID uuid.UUID,
	version int,
	attestations []TaskVersionHealthAttestation,
) (TaskVersionHealthState, error) {
	if taskID == uuid.Nil || version < 1 {
		return TaskVersionHealthState{}, fmt.Errorf("%w: invalid task health lookup", ErrTaskValidation)
	}
	if len(attestations) == 0 {
		return TaskVersionHealthState{}, nil
	}

	byRevision := make(map[int64]TaskVersionHealthAttestation, len(attestations))
	var latest TaskVersionHealthAttestation
	for _, attestation := range attestations {
		if err := attestation.Validate(); err != nil {
			return TaskVersionHealthState{}, err
		}
		if attestation.TaskID != taskID || attestation.Version != version {
			return TaskVersionHealthState{}, fmt.Errorf("%w: health attestation belongs to another task version", ErrTaskValidation)
		}
		if _, duplicate := byRevision[attestation.Revision]; duplicate {
			return TaskVersionHealthState{}, fmt.Errorf("%w: duplicate task health revision", ErrTaskValidation)
		}
		byRevision[attestation.Revision] = attestation
		if attestation.Revision > latest.Revision {
			latest = attestation
		}
	}
	for revision := int64(1); revision <= latest.Revision; revision++ {
		if _, found := byRevision[revision]; !found {
			return TaskVersionHealthState{}, fmt.Errorf("%w: missing task health revision", ErrTaskValidation)
		}
	}
	return TaskVersionHealthState{Attested: true, Healthy: latest.Healthy, Revision: latest.Revision}, nil
}
