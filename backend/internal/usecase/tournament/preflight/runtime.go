package preflight

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

const runtimeRevisionMaxLength = 128

var ErrInvalidCertification = errors.New("invalid capacity certification")

const (
	CodeRuntimeConfiguration Code = "tournament.preflight.runtime.configuration"
	CodeRuntimeStorage       Code = "tournament.preflight.runtime.authoritative_storage"
	CodeRuntimeSubmission    Code = "tournament.preflight.runtime.submission"
	CodeRuntimeTaskDelivery  Code = "tournament.preflight.runtime.task_delivery"
	CodeRuntimeRealtime      Code = "tournament.preflight.runtime.realtime"
	CodeRuntimeCapacity      Code = "tournament.preflight.runtime.capacity"
	CodeRuntimeClock         Code = "tournament.preflight.runtime.clock"
	CodeRuntimeDependencies  Code = "tournament.preflight.runtime.dependencies"
	CodeRuntimeSchedule      Code = "tournament.preflight.runtime.schedule"
)

type Certification struct {
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

type ConfigurationHealth struct {
	Valid    bool
	Revision string
}

type ComponentHealth struct {
	Healthy        bool
	Revision       string
	InternalDetail string
}

type Health struct {
	AuthoritativeStorage ComponentHealth
	Submission           ComponentHealth
	TaskDelivery         ComponentHealth
	Realtime             ComponentHealth
}

// RuntimeHealth contains the live, payload-free part of a preflight runtime
// report. Repository-loaded facts remain in RuntimeInput so adapters never
// need to construct process health.
type RuntimeHealth struct {
	TaskDelivery ComponentHealth
	Realtime     ComponentHealth
	Clock        ClockHealth
	ClockSampled bool
	Dependencies []DependencyHealth
}

type Dependency string

const (
	DependencyPostgres      Dependency = "postgres"
	DependencyRedis         Dependency = "redis"
	DependencyObjectStorage Dependency = "object_storage"
)

type DependencyHealth struct {
	Name           Dependency
	Healthy        bool
	Revision       string
	InternalDetail string
}

type ClockHealth struct {
	ObservedAt  time.Time
	ReferenceAt time.Time
	MaxSkew     time.Duration
}

type ScheduleHealth struct {
	StartsAt          time.Time
	MustFinishBy      time.Time
	ProjectedDuration time.Duration
}

type RuntimeInput struct {
	TournamentID    uuid.UUID
	Preset          domain.TournamentPreset
	RosterSize      int
	ContentRevision int64
	Configuration   ConfigurationHealth
	Health          Health
	Capacity        *Certification
	Clock           ClockHealth
	Dependencies    []DependencyHealth
	Schedule        ScheduleHealth
}

// ApplyRuntimeHealth combines pre-transaction runtime health with
// authoritative facts read by the repository inside the roster transaction.
// A sample cannot overwrite PostgreSQL-backed authority or submission facts.
func ApplyRuntimeHealth(input RuntimeInput, health RuntimeHealth) RuntimeInput {
	input.Health.TaskDelivery = health.TaskDelivery
	input.Health.Realtime = health.Realtime
	if health.ClockSampled {
		input.Clock = health.Clock
	}
	input.Dependencies = mergeRuntimeDependencies(input.Dependencies, health.Dependencies)
	return input
}

func mergeRuntimeDependencies(
	facts []DependencyHealth,
	health []DependencyHealth,
) []DependencyHealth {
	merged := make(map[Dependency]DependencyHealth, len(facts)+len(health))
	for _, dependency := range facts {
		merged[dependency.Name] = dependency
	}
	for _, dependency := range health {
		merged[dependency.Name] = dependency
	}
	result := make([]DependencyHealth, 0, len(merged))
	for _, name := range []Dependency{
		DependencyObjectStorage,
		DependencyPostgres,
		DependencyRedis,
	} {
		if dependency, exists := merged[name]; exists {
			result = append(result, dependency)
		}
	}
	return result
}

func NewCertification(
	id uuid.UUID,
	tournamentID uuid.UUID,
	contentRevision int64,
	normal capacity.NormalProof,
	golden capacity.GoldenProof,
	certifiedAt time.Time,
) (Certification, error) {
	certificate := Certification{
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
		return Certification{}, fmt.Errorf("%w: proofs do not certify the same roster and pools", ErrInvalidCertification)
	}
	if err := certificate.Validate(); err != nil {
		return Certification{}, err
	}
	return certificate, nil
}

func (c Certification) Validate() error {
	if c.ID == uuid.Nil || c.TournamentID == uuid.Nil || c.ContentRevision < 1 || c.RosterSize < 1 {
		return fmt.Errorf("%w: missing identity or revision", ErrInvalidCertification)
	}
	if c.NormalPoolRevisionID == uuid.Nil || c.NormalPoolRevision < 1 ||
		c.GoldenPoolRevisionID == uuid.Nil || c.GoldenPoolRevision < 1 ||
		c.NormalPoolRevisionID == c.GoldenPoolRevisionID {
		return fmt.Errorf("%w: invalid pool revisions", ErrInvalidCertification)
	}
	if !capacity.ValidProofDigest(c.NormalProofDigest) || !capacity.ValidProofDigest(c.GoldenProofDigest) ||
		c.NormalProofDigest == c.GoldenProofDigest {
		return fmt.Errorf("%w: invalid proof digest", ErrInvalidCertification)
	}
	if !validRuntimeTime(c.CertifiedAt) {
		return fmt.Errorf("%w: certification timestamp must be UTC", ErrInvalidCertification)
	}
	return nil
}

func EvaluateRuntime(in RuntimeInput) Report {
	return Report{Checks: []Check{
		checkRuntimeConfiguration(in),
		checkRuntimeComponent(
			CodeRuntimeStorage,
			"Authoritative storage accepts durable tournament reads and writes.",
			"authoritative_storage",
			in.Health.AuthoritativeStorage,
		),
		checkRuntimeComponent(
			CodeRuntimeSubmission,
			"Submission validation and durable intake are healthy.",
			"submission",
			in.Health.Submission,
		),
		checkRuntimeComponent(
			CodeRuntimeTaskDelivery,
			"Task snapshot and delivery dependencies are healthy.",
			"task_delivery",
			in.Health.TaskDelivery,
		),
		checkRuntimeComponent(
			CodeRuntimeRealtime,
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

func checkRuntimeConfiguration(in RuntimeInput) Check {
	issues := make([]string, 0)
	if in.TournamentID == uuid.Nil || !in.Preset.IsValid() || !in.Preset.ValidRosterSize(in.RosterSize) || in.ContentRevision < 1 {
		issues = append(issues, "tournament_configuration:invalid")
	}
	revision, revisionOK := normalizeRuntimeRevision(in.Configuration.Revision)
	if !in.Configuration.Valid || !revisionOK {
		issues = append(issues, "runtime_configuration:invalid")
	}
	return newPreflightCheck(
		CodeRuntimeConfiguration,
		"Runtime configuration is valid for the tournament preset and roster.",
		issues,
		[]string{"configuration_revision:" + revision, fmt.Sprintf("content_revision:%d", in.ContentRevision)},
	)
}

func checkRuntimeComponent(
	code Code,
	explanation string,
	name string,
	health ComponentHealth,
) Check {
	revision, revisionOK := normalizeRuntimeRevision(health.Revision)
	issues := make([]string, 0)
	if !health.Healthy || !revisionOK {
		issues = append(issues, name+":degraded")
	}
	return newPreflightCheck(code, explanation, issues, []string{name + "_revision:" + revision})
}

func checkRuntimeCapacity(in RuntimeInput) Check {
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
	return newPreflightCheck(
		CodeRuntimeCapacity,
		"Normal and Golden capacity proofs certify the current roster and content revision.",
		issues,
		evidence,
	)
}

func checkRuntimeClock(clock ClockHealth) Check {
	issues := make([]string, 0)
	skew := clock.ObservedAt.Sub(clock.ReferenceAt)
	if skew < 0 {
		skew = -skew
	}
	if !validRuntimeTime(clock.ObservedAt) || !validRuntimeTime(clock.ReferenceAt) ||
		clock.MaxSkew <= 0 || skew > clock.MaxSkew {
		issues = append(issues, "clock:outside_tolerance")
	}
	return newPreflightCheck(
		CodeRuntimeClock,
		"Server clock is UTC and within the configured authoritative tolerance.",
		issues,
		[]string{fmt.Sprintf("max_skew_ms:%d", clock.MaxSkew.Milliseconds()), fmt.Sprintf("observed_skew_ms:%d", skew.Milliseconds())},
	)
}

func checkRuntimeDependencies(input []DependencyHealth) Check {
	required := []Dependency{
		DependencyObjectStorage,
		DependencyPostgres,
		DependencyRedis,
	}
	indexed := make(map[Dependency]DependencyHealth, len(input))
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
	return newPreflightCheck(
		CodeRuntimeDependencies,
		"PostgreSQL, Redis, and object storage dependencies are healthy.",
		issues,
		evidence,
	)
}

func checkRuntimeSchedule(preset domain.TournamentPreset, schedule ScheduleHealth) Check {
	issues := make([]string, 0)
	available := schedule.MustFinishBy.Sub(schedule.StartsAt)
	nominal := preset.NominalDuration()
	if !validRuntimeTime(schedule.StartsAt) || !validRuntimeTime(schedule.MustFinishBy) ||
		available <= 0 || schedule.ProjectedDuration <= 0 || nominal <= 0 ||
		schedule.ProjectedDuration > available || schedule.ProjectedDuration > nominal {
		issues = append(issues, "schedule:outside_nominal_window")
	}
	return newPreflightCheck(
		CodeRuntimeSchedule,
		"Projected execution fits both the scheduled window and the nominal preset duration.",
		issues,
		[]string{
			fmt.Sprintf("available_seconds:%d", int64(available.Seconds())),
			fmt.Sprintf("nominal_seconds:%d", int64(nominal.Seconds())),
			fmt.Sprintf("projected_seconds:%d", int64(schedule.ProjectedDuration.Seconds())),
		},
	)
}

func (d Dependency) IsValid() bool {
	switch d {
	case DependencyPostgres, DependencyRedis, DependencyObjectStorage:
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

func validRuntimeTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}
