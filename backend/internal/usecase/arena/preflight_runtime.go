package arena

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const runtimeRevisionMaxLength = 128

var ErrInvalidCapacityCertification = errors.New("invalid arena capacity certification")

const (
	PreflightCodeRuntimeConfiguration ArenaPreflightCode = "arena.preflight.runtime.configuration"
	PreflightCodeRuntimeStorage       ArenaPreflightCode = "arena.preflight.runtime.authoritative_storage"
	PreflightCodeRuntimeSubmission    ArenaPreflightCode = "arena.preflight.runtime.submission"
	PreflightCodeRuntimeTaskDelivery  ArenaPreflightCode = "arena.preflight.runtime.task_delivery"
	PreflightCodeRuntimeRealtime      ArenaPreflightCode = "arena.preflight.runtime.realtime"
	PreflightCodeRuntimeCapacity      ArenaPreflightCode = "arena.preflight.runtime.capacity"
	PreflightCodeRuntimeClock         ArenaPreflightCode = "arena.preflight.runtime.clock"
	PreflightCodeRuntimeDependencies  ArenaPreflightCode = "arena.preflight.runtime.dependencies"
	PreflightCodeRuntimeSchedule      ArenaPreflightCode = "arena.preflight.runtime.schedule"
)

type CapacityCertification struct {
	ID                   uuid.UUID
	TournamentID         uuid.UUID
	ContentRevision      int64
	RosterSize           int
	NormalPoolRevisionID uuid.UUID
	NormalPoolRevision   int64
	GoldenPoolRevisionID uuid.UUID
	GoldenPoolRevision   int64
	NormalProofDigest    string
	GoldenProofDigest    string
	CertifiedAt          time.Time
}

type RuntimeConfigurationHealth struct {
	Valid    bool
	Revision string
}

type RuntimeComponentHealth struct {
	Healthy        bool
	Revision       string
	InternalDetail string
}

type RuntimeHealth struct {
	AuthoritativeStorage RuntimeComponentHealth
	Submission           RuntimeComponentHealth
	TaskDelivery         RuntimeComponentHealth
	Realtime             RuntimeComponentHealth
}

type RuntimeDependency string

const (
	RuntimeDependencyPostgres      RuntimeDependency = "postgres"
	RuntimeDependencyRedis         RuntimeDependency = "redis"
	RuntimeDependencyObjectStorage RuntimeDependency = "object_storage"
)

type RuntimeDependencyHealth struct {
	Name           RuntimeDependency
	Healthy        bool
	Revision       string
	InternalDetail string
}

type RuntimeClockHealth struct {
	ObservedAt  time.Time
	ReferenceAt time.Time
	MaxSkew     time.Duration
}

type RuntimeScheduleHealth struct {
	StartsAt          time.Time
	MustFinishBy      time.Time
	ProjectedDuration time.Duration
}

type RuntimePreflightInput struct {
	TournamentID    uuid.UUID
	Preset          domain.ArenaPreset
	RosterSize      int
	ContentRevision int64
	Configuration   RuntimeConfigurationHealth
	Health          RuntimeHealth
	Capacity        *CapacityCertification
	Clock           RuntimeClockHealth
	Dependencies    []RuntimeDependencyHealth
	Schedule        RuntimeScheduleHealth
}

func NewCapacityCertification(
	id uuid.UUID,
	tournamentID uuid.UUID,
	contentRevision int64,
	normal NormalCapacityProof,
	golden GoldenCapacityProof,
	certifiedAt time.Time,
) (CapacityCertification, error) {
	certificate := CapacityCertification{
		ID:                   id,
		TournamentID:         tournamentID,
		ContentRevision:      contentRevision,
		RosterSize:           normal.RosterSize,
		NormalPoolRevisionID: normal.PoolRevisionID,
		NormalPoolRevision:   normal.PoolRevision,
		GoldenPoolRevisionID: golden.PoolRevisionID,
		GoldenPoolRevision:   golden.PoolRevision,
		NormalProofDigest:    normal.Digest,
		GoldenProofDigest:    golden.Digest,
		CertifiedAt:          certifiedAt,
	}
	if !normal.Certified || normal.Failure != nil || !golden.Certified || golden.Failure != nil ||
		normal.RosterSize != golden.RosterSize || normal.PoolRevisionID != golden.NormalPoolRevisionID ||
		normal.PoolRevision != golden.NormalPoolRevision {
		return CapacityCertification{}, fmt.Errorf("%w: proofs do not certify the same roster and pools", ErrInvalidCapacityCertification)
	}
	if err := certificate.Validate(); err != nil {
		return CapacityCertification{}, err
	}
	return certificate, nil
}

func (c CapacityCertification) Validate() error {
	if c.ID == uuid.Nil || c.TournamentID == uuid.Nil || c.ContentRevision < 1 || c.RosterSize < 1 {
		return fmt.Errorf("%w: missing identity or revision", ErrInvalidCapacityCertification)
	}
	if c.NormalPoolRevisionID == uuid.Nil || c.NormalPoolRevision < 1 ||
		c.GoldenPoolRevisionID == uuid.Nil || c.GoldenPoolRevision < 1 ||
		c.NormalPoolRevisionID == c.GoldenPoolRevisionID {
		return fmt.Errorf("%w: invalid pool revisions", ErrInvalidCapacityCertification)
	}
	if !validCapacityDigest(c.NormalProofDigest) || !validCapacityDigest(c.GoldenProofDigest) ||
		c.NormalProofDigest == c.GoldenProofDigest {
		return fmt.Errorf("%w: invalid proof digest", ErrInvalidCapacityCertification)
	}
	if !validRuntimeTime(c.CertifiedAt) {
		return fmt.Errorf("%w: certification timestamp must be UTC", ErrInvalidCapacityCertification)
	}
	return nil
}

func EvaluateRuntimePreflight(in RuntimePreflightInput) ArenaPreflightReport {
	return ArenaPreflightReport{Checks: []ArenaPreflightCheck{
		checkRuntimeConfiguration(in),
		checkRuntimeComponent(
			PreflightCodeRuntimeStorage,
			"Authoritative storage accepts durable Arena reads and writes.",
			"authoritative_storage",
			in.Health.AuthoritativeStorage,
		),
		checkRuntimeComponent(
			PreflightCodeRuntimeSubmission,
			"Submission validation and durable intake are healthy.",
			"submission",
			in.Health.Submission,
		),
		checkRuntimeComponent(
			PreflightCodeRuntimeTaskDelivery,
			"Task snapshot and delivery dependencies are healthy.",
			"task_delivery",
			in.Health.TaskDelivery,
		),
		checkRuntimeComponent(
			PreflightCodeRuntimeRealtime,
			"Realtime delivery and recovery dependencies are healthy.",
			"realtime",
			in.Health.Realtime,
		),
		checkRuntimeCapacity(in),
		checkRuntimeClock(in.Clock),
		checkRuntimeDependencies(in.Dependencies),
		checkRuntimeSchedule(in.Preset, in.Schedule),
	}}
}

func checkRuntimeConfiguration(in RuntimePreflightInput) ArenaPreflightCheck {
	issues := make([]string, 0)
	if in.TournamentID == uuid.Nil || !in.Preset.IsValid() || !in.Preset.ValidRosterSize(in.RosterSize) || in.ContentRevision < 1 {
		issues = append(issues, "tournament_configuration:invalid")
	}
	revision, revisionOK := normalizeRuntimeRevision(in.Configuration.Revision)
	if !in.Configuration.Valid || !revisionOK {
		issues = append(issues, "runtime_configuration:invalid")
	}
	return newArenaPreflightCheck(
		PreflightCodeRuntimeConfiguration,
		"Runtime configuration is valid for the tournament preset and roster.",
		issues,
		[]string{"configuration_revision:" + revision, fmt.Sprintf("content_revision:%d", in.ContentRevision)},
	)
}

func checkRuntimeComponent(
	code ArenaPreflightCode,
	explanation string,
	name string,
	health RuntimeComponentHealth,
) ArenaPreflightCheck {
	revision, revisionOK := normalizeRuntimeRevision(health.Revision)
	issues := make([]string, 0)
	if !health.Healthy || !revisionOK {
		issues = append(issues, name+":degraded")
	}
	return newArenaPreflightCheck(code, explanation, issues, []string{name + "_revision:" + revision})
}

func checkRuntimeCapacity(in RuntimePreflightInput) ArenaPreflightCheck {
	issues := make([]string, 0)
	evidence := make([]string, 0, 3)
	if in.Capacity == nil {
		issues = append(issues, "capacity_certification:missing")
	} else {
		capacity := *in.Capacity
		if capacity.Validate() != nil || capacity.TournamentID != in.TournamentID ||
			capacity.ContentRevision != in.ContentRevision || capacity.RosterSize != in.RosterSize ||
			(validRuntimeTime(in.Clock.ObservedAt) && capacity.CertifiedAt.After(in.Clock.ObservedAt)) {
			issues = append(issues, "capacity_certification:stale_or_invalid")
		} else {
			evidence = append(evidence,
				"capacity_revision:"+capacity.ID.String(),
				fmt.Sprintf("normal_pool:%s@%d", capacity.NormalPoolRevisionID, capacity.NormalPoolRevision),
				fmt.Sprintf("golden_pool:%s@%d", capacity.GoldenPoolRevisionID, capacity.GoldenPoolRevision),
			)
		}
	}
	return newArenaPreflightCheck(
		PreflightCodeRuntimeCapacity,
		"Normal and Golden capacity proofs certify the current roster and content revision.",
		issues,
		evidence,
	)
}

func checkRuntimeClock(clock RuntimeClockHealth) ArenaPreflightCheck {
	issues := make([]string, 0)
	skew := clock.ObservedAt.Sub(clock.ReferenceAt)
	if skew < 0 {
		skew = -skew
	}
	if !validRuntimeTime(clock.ObservedAt) || !validRuntimeTime(clock.ReferenceAt) ||
		clock.MaxSkew <= 0 || skew > clock.MaxSkew {
		issues = append(issues, "clock:outside_tolerance")
	}
	return newArenaPreflightCheck(
		PreflightCodeRuntimeClock,
		"Server clock is UTC and within the configured authoritative tolerance.",
		issues,
		[]string{fmt.Sprintf("max_skew_ms:%d", clock.MaxSkew.Milliseconds()), fmt.Sprintf("observed_skew_ms:%d", skew.Milliseconds())},
	)
}

func checkRuntimeDependencies(input []RuntimeDependencyHealth) ArenaPreflightCheck {
	required := []RuntimeDependency{
		RuntimeDependencyObjectStorage,
		RuntimeDependencyPostgres,
		RuntimeDependencyRedis,
	}
	indexed := make(map[RuntimeDependency]RuntimeDependencyHealth, len(input))
	issues := make([]string, 0)
	for _, dependency := range input {
		if !dependency.Name.IsValid() {
			issues = append(issues, "dependency:unknown")
			continue
		}
		if _, duplicate := indexed[dependency.Name]; duplicate {
			issues = append(issues, string(dependency.Name)+":duplicate")
			continue
		}
		indexed[dependency.Name] = dependency
	}
	evidence := make([]string, 0, len(required))
	for _, name := range required {
		dependency, exists := indexed[name]
		revision, revisionOK := normalizeRuntimeRevision(dependency.Revision)
		if !exists {
			issues = append(issues, string(name)+":missing")
			continue
		}
		if !dependency.Healthy || !revisionOK {
			issues = append(issues, string(name)+":degraded")
			continue
		}
		evidence = append(evidence, string(name)+"_revision:"+revision)
	}
	return newArenaPreflightCheck(
		PreflightCodeRuntimeDependencies,
		"PostgreSQL, Redis, and object storage dependencies are healthy.",
		issues,
		evidence,
	)
}

func checkRuntimeSchedule(preset domain.ArenaPreset, schedule RuntimeScheduleHealth) ArenaPreflightCheck {
	issues := make([]string, 0)
	available := schedule.MustFinishBy.Sub(schedule.StartsAt)
	nominal := preset.NominalDuration()
	if !validRuntimeTime(schedule.StartsAt) || !validRuntimeTime(schedule.MustFinishBy) ||
		available <= 0 || schedule.ProjectedDuration <= 0 || nominal <= 0 ||
		schedule.ProjectedDuration > available || schedule.ProjectedDuration > nominal {
		issues = append(issues, "schedule:outside_nominal_window")
	}
	return newArenaPreflightCheck(
		PreflightCodeRuntimeSchedule,
		"Projected execution fits both the scheduled window and the nominal preset duration.",
		issues,
		[]string{
			fmt.Sprintf("available_seconds:%d", int64(available.Seconds())),
			fmt.Sprintf("nominal_seconds:%d", int64(nominal.Seconds())),
			fmt.Sprintf("projected_seconds:%d", int64(schedule.ProjectedDuration.Seconds())),
		},
	)
}

func (d RuntimeDependency) IsValid() bool {
	switch d {
	case RuntimeDependencyPostgres, RuntimeDependencyRedis, RuntimeDependencyObjectStorage:
		return true
	}
	return false
}

func normalizeRuntimeRevision(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > runtimeRevisionMaxLength {
		return value, false
	}
	for _, character := range value {
		if !runtimeRevisionCharacterAllowed(character) {
			return value, false
		}
	}
	return value, true
}

func runtimeRevisionCharacterAllowed(character rune) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' ||
		strings.ContainsRune("._:/@+-", character)
}

func validCapacityDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validRuntimeTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}
