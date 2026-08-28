package arena

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const PreflightReportAlgorithmV1 = "arena-preflight-report-v1"

var ErrInvalidPreflightReport = errors.New("arena: invalid preflight report")

type PreflightSourceRevision struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

type PreflightReportInput struct {
	RosterRevision  int64
	PairingRevision int64
	Structural      StructuralPreflightInput
	TaskHealth      TaskHealthPreflightInput
	Runtime         RuntimePreflightInput
}

type PreflightReportRevision struct {
	ID               uuid.UUID                 `json:"id"`
	TournamentID     uuid.UUID                 `json:"tournament_id"`
	AlgorithmVersion string                    `json:"algorithm_version"`
	EvaluatedAt      time.Time                 `json:"evaluated_at"`
	NormalizedInputs []string                  `json:"normalized_inputs"`
	Revisions        []PreflightSourceRevision `json:"revisions"`
	ProofHash        string                    `json:"proof_hash"`
	Checks           []ArenaPreflightCheck     `json:"checks"`
}

type preflightProofDocument struct {
	AlgorithmVersion string                    `json:"algorithm_version"`
	TournamentID     string                    `json:"tournament_id"`
	NormalizedInputs []string                  `json:"normalized_inputs"`
	Revisions        []PreflightSourceRevision `json:"revisions"`
	Checks           []preflightProofCheck     `json:"checks"`
}

type preflightStructuralDocument struct {
	TournamentID       string                              `json:"tournament_id"`
	Preset             string                              `json:"preset"`
	ExpectedRosterSize int                                 `json:"expected_roster_size"`
	Participants       []preflightParticipantDocument      `json:"participants"`
	CategoryPools      []preflightCategoryPoolDocument     `json:"category_pools"`
	Pairings           []preflightPairDocument             `json:"pairings"`
	ByeParticipantID   string                              `json:"bye_participant_id"`
	RepeatedPairings   []preflightPairDocument             `json:"repeated_pairings"`
	Overrides          []preflightOverrideEvidenceDocument `json:"overrides"`
}

type preflightTaskHealthInputDocument struct {
	NormalPool preflightTaskPoolDocument            `json:"normal_pool"`
	GoldenPool preflightTaskPoolDocument            `json:"golden_pool"`
	Versions   []preflightTaskVersionHealthDocument `json:"versions"`
}

type preflightRuntimeDocument struct {
	TournamentID    string                                `json:"tournament_id"`
	Preset          string                                `json:"preset"`
	RosterSize      int                                   `json:"roster_size"`
	ContentRevision int64                                 `json:"content_revision"`
	Configuration   preflightRuntimeConfigurationDocument `json:"configuration"`
	Health          preflightRuntimeHealthDocument        `json:"health"`
	Capacity        *preflightCapacityDocument            `json:"capacity"`
	Clock           preflightClockDocument                `json:"clock"`
	Dependencies    []preflightDependencyDocument         `json:"dependencies"`
	Schedule        preflightScheduleDocument             `json:"schedule"`
}

type preflightProofCheck struct {
	Code        string   `json:"code"`
	Passed      bool     `json:"passed"`
	Explanation string   `json:"explanation"`
	Evidence    []string `json:"evidence"`
}

type preflightParticipantDocument struct {
	ParticipantID        string `json:"participant_id"`
	PlayerID             string `json:"player_id"`
	Seed                 int    `json:"seed"`
	Attendance           string `json:"attendance"`
	ReservedTournamentID string `json:"reserved_tournament_id"`
}

type preflightCategoryPoolDocument struct {
	ID         string   `json:"id"`
	Revision   int64    `json:"revision"`
	Format     string   `json:"format"`
	Categories []string `json:"categories"`
}

type preflightPairDocument struct {
	FirstParticipantID  string `json:"first_participant_id"`
	SecondParticipantID string `json:"second_participant_id"`
}

type preflightOverrideEvidenceDocument struct {
	Pair      preflightPairDocument `json:"pair"`
	ActorID   string                `json:"actor_id"`
	Confirmed bool                  `json:"confirmed"`
	Reason    string                `json:"reason"`
}

type preflightTaskVersionDocument struct {
	TaskID  string `json:"task_id"`
	Version int    `json:"version"`
}

type preflightTaskPoolDocument struct {
	ID       string                         `json:"id"`
	Revision int64                          `json:"revision"`
	Kind     string                         `json:"kind"`
	Versions []preflightTaskVersionDocument `json:"versions"`
}

type preflightTaskVersionHealthDocument struct {
	TaskID          string `json:"task_id"`
	Version         int    `json:"version"`
	PoolRevisionID  string `json:"pool_revision_id"`
	PoolKind        string `json:"pool_kind"`
	Exists          bool   `json:"exists"`
	Enabled         bool   `json:"enabled"`
	Healthy         bool   `json:"healthy"`
	MutationLocked  bool   `json:"mutation_locked"`
	PubliclyExposed bool   `json:"publicly_exposed"`
}

type preflightRuntimeConfigurationDocument struct {
	Valid    bool   `json:"valid"`
	Revision string `json:"revision"`
}

type preflightRuntimeComponentDocument struct {
	Healthy  bool   `json:"healthy"`
	Revision string `json:"revision"`
}

type preflightRuntimeHealthDocument struct {
	AuthoritativeStorage preflightRuntimeComponentDocument `json:"authoritative_storage"`
	Submission           preflightRuntimeComponentDocument `json:"submission"`
	TaskDelivery         preflightRuntimeComponentDocument `json:"task_delivery"`
	Realtime             preflightRuntimeComponentDocument `json:"realtime"`
}

type preflightCapacityDocument struct {
	ID                   string `json:"id"`
	TournamentID         string `json:"tournament_id"`
	ContentRevision      int64  `json:"content_revision"`
	RosterSize           int    `json:"roster_size"`
	NormalPoolRevisionID string `json:"normal_pool_revision_id"`
	NormalPoolRevision   int64  `json:"normal_pool_revision"`
	GoldenPoolRevisionID string `json:"golden_pool_revision_id"`
	GoldenPoolRevision   int64  `json:"golden_pool_revision"`
	NormalProofDigest    string `json:"normal_proof_digest"`
	GoldenProofDigest    string `json:"golden_proof_digest"`
	CertifiedAt          string `json:"certified_at"`
}

type preflightClockDocument struct {
	ObservedAt  string `json:"observed_at"`
	ReferenceAt string `json:"reference_at"`
	MaxSkew     int64  `json:"max_skew_nanoseconds"`
}

type preflightDependencyDocument struct {
	Name     string `json:"name"`
	Healthy  bool   `json:"healthy"`
	Revision string `json:"revision"`
}

type preflightScheduleDocument struct {
	StartsAt          string `json:"starts_at"`
	MustFinishBy      string `json:"must_finish_by"`
	ProjectedDuration int64  `json:"projected_duration_nanoseconds"`
}

var preflightReportCheckCodes = []ArenaPreflightCode{
	PreflightCodeRosterComplete,
	PreflightCodeAttendance,
	PreflightCodeParticipantExclusive,
	PreflightCodePreset,
	PreflightCodeCategories,
	PreflightCodePairings,
	PreflightCodeByes,
	PreflightCodeOverrides,
	PreflightCodeTaskPoolsValid,
	PreflightCodeTaskInventory,
	PreflightCodeTaskMissing,
	PreflightCodeTaskDisabled,
	PreflightCodeTaskUnhealthy,
	PreflightCodeTaskMutable,
	PreflightCodeTaskExposed,
	PreflightCodeTaskWrongPool,
	PreflightCodeRuntimeConfiguration,
	PreflightCodeRuntimeStorage,
	PreflightCodeRuntimeSubmission,
	PreflightCodeRuntimeTaskDelivery,
	PreflightCodeRuntimeRealtime,
	PreflightCodeRuntimeCapacity,
	PreflightCodeRuntimeClock,
	PreflightCodeRuntimeDependencies,
	PreflightCodeRuntimeSchedule,
}

func NewPreflightReportRevision(
	id uuid.UUID,
	evaluatedAt time.Time,
	in PreflightReportInput,
) (PreflightReportRevision, error) {
	if id == uuid.Nil || !validRuntimeTime(evaluatedAt) || in.RosterRevision < 1 || in.PairingRevision < 1 {
		return PreflightReportRevision{}, preflightReportError("missing identity, UTC timestamp, or source revision")
	}

	report, err := composePreflightReportRevision(id, evaluatedAt, in)
	if err != nil {
		return PreflightReportRevision{}, err
	}
	if err := report.Validate(); err != nil {
		return PreflightReportRevision{}, err
	}
	return report, nil
}

func (r PreflightReportRevision) Passed() bool {
	return ArenaPreflightReport{Checks: r.Checks}.Passed()
}

func (r PreflightReportRevision) InvalidatedBy(in PreflightReportInput) bool {
	if r.Validate() != nil {
		return true
	}
	candidate, err := composePreflightReportRevision(r.ID, r.EvaluatedAt, in)
	return err != nil || candidate.ProofHash != r.ProofHash
}

func (r PreflightReportRevision) Validate() error {
	if r.ID == uuid.Nil || r.TournamentID == uuid.Nil || !validRuntimeTime(r.EvaluatedAt) ||
		r.AlgorithmVersion != PreflightReportAlgorithmV1 || !validCapacityDigest(r.ProofHash) {
		return preflightReportError("invalid revision identity, algorithm, timestamp, or proof hash")
	}
	if !sortedUniqueOperatorStrings(r.NormalizedInputs, false) {
		return preflightReportError("normalized inputs are incomplete or not canonical")
	}
	if !validPreflightSourceRevisions(r.Revisions) {
		return preflightReportError("source revisions are incomplete or not canonical")
	}
	if !validPreflightChecks(r.Checks) {
		return preflightReportError("checks are incomplete or not canonical")
	}

	want, err := preflightProofHash(newPreflightProofDocument(
		r.AlgorithmVersion,
		r.TournamentID,
		r.NormalizedInputs,
		r.Revisions,
		r.Checks,
	))
	if err != nil || want != r.ProofHash {
		return preflightReportError("proof hash does not match the report")
	}
	return nil
}

func composePreflightReportRevision(
	id uuid.UUID,
	evaluatedAt time.Time,
	in PreflightReportInput,
) (PreflightReportRevision, error) {
	if in.RosterRevision < 1 || in.PairingRevision < 1 {
		return PreflightReportRevision{}, preflightReportError("source revisions must be positive")
	}
	normalized := normalizePreflightReportInput(in)
	tournamentID := normalized.Structural.TournamentID
	if tournamentID == uuid.Nil {
		tournamentID = normalized.Runtime.TournamentID
	}
	if tournamentID == uuid.Nil {
		return PreflightReportRevision{}, preflightReportError("missing tournament identity")
	}

	structural := EvaluateStructuralPreflight(normalized.Structural)
	tasks := EvaluateTaskHealthPreflight(normalized.TaskHealth)
	runtime := EvaluateRuntimePreflight(normalized.Runtime)
	if !preflightSourcesAligned(normalized) {
		runtime.Checks[0].Passed = false
		runtime.Checks[0].Evidence = append(runtime.Checks[0].Evidence, "source_alignment:mismatch")
		runtime.Checks[0].Evidence = sortedUniqueStrings(runtime.Checks[0].Evidence)
	}
	checks := make([]ArenaPreflightCheck, 0, len(structural.Checks)+len(tasks.Checks)+len(runtime.Checks))
	checks = appendClonedPreflightChecks(checks, structural.Checks)
	checks = appendClonedPreflightChecks(checks, tasks.Checks)
	checks = appendClonedPreflightChecks(checks, runtime.Checks)

	normalizedInputs, err := preflightNormalizedInputs(normalized)
	if err != nil {
		return PreflightReportRevision{}, err
	}
	revisions := preflightSourceRevisions(normalized)
	document := newPreflightProofDocument(
		PreflightReportAlgorithmV1,
		tournamentID,
		normalizedInputs,
		revisions,
		checks,
	)
	proofHash, err := preflightProofHash(document)
	if err != nil {
		return PreflightReportRevision{}, err
	}
	return PreflightReportRevision{
		ID:               id,
		TournamentID:     tournamentID,
		AlgorithmVersion: PreflightReportAlgorithmV1,
		EvaluatedAt:      evaluatedAt,
		NormalizedInputs: normalizedInputs,
		Revisions:        revisions,
		ProofHash:        proofHash,
		Checks:           checks,
	}, nil
}

func preflightSourcesAligned(in PreflightReportInput) bool {
	baseAligned := in.Structural.TournamentID != uuid.Nil &&
		in.Structural.TournamentID == in.Runtime.TournamentID &&
		in.Structural.Preset == in.Runtime.Preset &&
		in.Structural.ExpectedRosterSize == in.Runtime.RosterSize
	if !baseAligned || in.Runtime.Capacity == nil {
		return baseAligned
	}
	return in.TaskHealth.NormalPool.ID == in.Runtime.Capacity.NormalPoolRevisionID &&
		in.TaskHealth.NormalPool.Revision == in.Runtime.Capacity.NormalPoolRevision &&
		in.TaskHealth.GoldenPool.ID == in.Runtime.Capacity.GoldenPoolRevisionID &&
		in.TaskHealth.GoldenPool.Revision == in.Runtime.Capacity.GoldenPoolRevision
}

func normalizePreflightReportInput(in PreflightReportInput) PreflightReportInput {
	in.Structural = normalizeStructuralReportInput(in.Structural)
	in.TaskHealth = normalizeTaskHealthReportInput(in.TaskHealth)
	in.Runtime = normalizeRuntimeReportInput(in.Runtime)
	return in
}

func normalizeStructuralReportInput(in StructuralPreflightInput) StructuralPreflightInput {
	in.Preset = domain.ArenaPreset(safeTypedValue(in.Preset.String(), in.Preset.IsValid()))
	in.Participants = append([]StructuralParticipant{}, in.Participants...)
	for i := range in.Participants {
		attendance := in.Participants[i].Attendance
		in.Participants[i].Attendance = domain.ArenaAttendanceState(
			safeTypedValue(string(attendance), attendance.IsValid()),
		)
	}
	sort.Slice(in.Participants, func(i, j int) bool {
		return structuralParticipantLess(in.Participants[i], in.Participants[j])
	})

	in.CategoryPools = append([]CategoryPoolRevision{}, in.CategoryPools...)
	for i := range in.CategoryPools {
		format := in.CategoryPools[i].Format
		in.CategoryPools[i].Format = domain.ArenaSeriesFormat(
			safeTypedValue(string(format), format.IsValid()),
		)
		in.CategoryPools[i].Categories = append([]domain.Category{}, in.CategoryPools[i].Categories...)
		for categoryIndex := range in.CategoryPools[i].Categories {
			category := in.CategoryPools[i].Categories[categoryIndex]
			in.CategoryPools[i].Categories[categoryIndex] = domain.Category(
				safeTypedValue(string(category), category.IsValid()),
			)
		}
		sort.Slice(in.CategoryPools[i].Categories, func(first, second int) bool {
			return in.CategoryPools[i].Categories[first] < in.CategoryPools[i].Categories[second]
		})
	}
	sort.Slice(in.CategoryPools, func(i, j int) bool {
		if in.CategoryPools[i].Format != in.CategoryPools[j].Format {
			return in.CategoryPools[i].Format < in.CategoryPools[j].Format
		}
		if comparison := bytes.Compare(in.CategoryPools[i].ID[:], in.CategoryPools[j].ID[:]); comparison != 0 {
			return comparison < 0
		}
		return in.CategoryPools[i].Revision < in.CategoryPools[j].Revision
	})

	in.Pairings = normalizeSwissPairs(in.Pairings)
	in.RepeatedPairings = normalizeSwissPairs(in.RepeatedPairings)
	in.Overrides = append([]StructuralOverrideEvidence{}, in.Overrides...)
	for i := range in.Overrides {
		in.Overrides[i].Pair = normalizeSwissPair(in.Overrides[i].Pair)
		if strings.TrimSpace(in.Overrides[i].Reason) == "" {
			in.Overrides[i].Reason = ""
		} else {
			in.Overrides[i].Reason = "recorded"
		}
	}
	sort.Slice(in.Overrides, func(i, j int) bool {
		return structuralOverrideLess(in.Overrides[i], in.Overrides[j])
	})
	return in
}

func normalizeTaskHealthReportInput(in TaskHealthPreflightInput) TaskHealthPreflightInput {
	in.NormalPool = normalizeTaskPoolReportInput(in.NormalPool)
	in.GoldenPool = normalizeTaskPoolReportInput(in.GoldenPool)
	in.Versions = append([]TaskVersionHealth{}, in.Versions...)
	for i := range in.Versions {
		kind := in.Versions[i].PoolKind
		in.Versions[i].PoolKind = domain.ArenaTaskKind(safeTypedValue(string(kind), kind.IsValid()))
		in.Versions[i].InternalHealthDetail = ""
	}
	sort.Slice(in.Versions, func(i, j int) bool {
		return taskVersionHealthLess(in.Versions[i], in.Versions[j])
	})
	return in
}

func normalizeTaskPoolReportInput(in TaskPoolRevision) TaskPoolRevision {
	in.Kind = domain.ArenaTaskKind(safeTypedValue(string(in.Kind), in.Kind.IsValid()))
	in.Versions = append([]TaskVersionRef{}, in.Versions...)
	sort.Slice(in.Versions, func(i, j int) bool {
		if comparison := bytes.Compare(in.Versions[i].TaskID[:], in.Versions[j].TaskID[:]); comparison != 0 {
			return comparison < 0
		}
		return in.Versions[i].Version < in.Versions[j].Version
	})
	return in
}

func normalizeRuntimeReportInput(in RuntimePreflightInput) RuntimePreflightInput {
	in.Preset = domain.ArenaPreset(safeTypedValue(in.Preset.String(), in.Preset.IsValid()))
	in.Configuration.Revision = safeRuntimeRevision(in.Configuration.Revision)
	in.Health.AuthoritativeStorage = normalizeRuntimeComponent(in.Health.AuthoritativeStorage)
	in.Health.Submission = normalizeRuntimeComponent(in.Health.Submission)
	in.Health.TaskDelivery = normalizeRuntimeComponent(in.Health.TaskDelivery)
	in.Health.Realtime = normalizeRuntimeComponent(in.Health.Realtime)
	if in.Capacity != nil {
		capacity := *in.Capacity
		capacity.NormalProofDigest = safeCapacityProofDigest(capacity.NormalProofDigest)
		capacity.GoldenProofDigest = safeCapacityProofDigest(capacity.GoldenProofDigest)
		in.Capacity = &capacity
	}
	in.Dependencies = append([]RuntimeDependencyHealth{}, in.Dependencies...)
	for i := range in.Dependencies {
		name := in.Dependencies[i].Name
		in.Dependencies[i].Name = RuntimeDependency(safeTypedValue(string(name), name.IsValid()))
		in.Dependencies[i].Revision = safeRuntimeRevision(in.Dependencies[i].Revision)
		in.Dependencies[i].InternalDetail = ""
	}
	sort.Slice(in.Dependencies, func(i, j int) bool {
		if in.Dependencies[i].Name != in.Dependencies[j].Name {
			return in.Dependencies[i].Name < in.Dependencies[j].Name
		}
		if in.Dependencies[i].Revision != in.Dependencies[j].Revision {
			return in.Dependencies[i].Revision < in.Dependencies[j].Revision
		}
		return !in.Dependencies[i].Healthy && in.Dependencies[j].Healthy
	})
	return in
}

func normalizeRuntimeComponent(in RuntimeComponentHealth) RuntimeComponentHealth {
	in.Revision = safeRuntimeRevision(in.Revision)
	in.InternalDetail = ""
	return in
}

func newPreflightProofDocument(
	algorithmVersion string,
	tournamentID uuid.UUID,
	normalizedInputs []string,
	revisions []PreflightSourceRevision,
	checks []ArenaPreflightCheck,
) preflightProofDocument {
	proofChecks := make([]preflightProofCheck, 0, len(checks))
	for _, check := range checks {
		proofChecks = append(proofChecks, preflightProofCheck{
			Code:        string(check.Code),
			Passed:      check.Passed,
			Explanation: check.Explanation,
			Evidence:    append([]string{}, check.Evidence...),
		})
	}
	return preflightProofDocument{
		AlgorithmVersion: algorithmVersion,
		TournamentID:     tournamentID.String(),
		NormalizedInputs: append([]string{}, normalizedInputs...),
		Revisions:        append([]PreflightSourceRevision{}, revisions...),
		Checks:           proofChecks,
	}
}

func preflightNormalizedInputs(in PreflightReportInput) ([]string, error) {
	structural, err := json.Marshal(newPreflightStructuralDocument(in.Structural))
	if err != nil {
		return nil, preflightReportError("cannot encode normalized structural input")
	}
	tasks, err := json.Marshal(newPreflightTaskHealthDocument(in.TaskHealth))
	if err != nil {
		return nil, preflightReportError("cannot encode normalized task input")
	}
	runtime, err := json.Marshal(newPreflightRuntimeDocument(in.Runtime))
	if err != nil {
		return nil, preflightReportError("cannot encode normalized runtime input")
	}
	result := []string{
		"pairing_revision:" + strconv.FormatInt(in.PairingRevision, 10),
		"roster_revision:" + strconv.FormatInt(in.RosterRevision, 10),
		"runtime:" + string(runtime),
		"runtime.capacity.certified_at_utc:" + strconv.FormatBool(in.Runtime.Capacity != nil && validRuntimeTime(in.Runtime.Capacity.CertifiedAt)),
		"runtime.clock.observed_at_utc:" + strconv.FormatBool(validRuntimeTime(in.Runtime.Clock.ObservedAt)),
		"runtime.clock.reference_at_utc:" + strconv.FormatBool(validRuntimeTime(in.Runtime.Clock.ReferenceAt)),
		"runtime.schedule.must_finish_by_utc:" + strconv.FormatBool(validRuntimeTime(in.Runtime.Schedule.MustFinishBy)),
		"runtime.schedule.starts_at_utc:" + strconv.FormatBool(validRuntimeTime(in.Runtime.Schedule.StartsAt)),
		"structural:" + string(structural),
		"tasks:" + string(tasks),
	}
	sort.Strings(result)
	return result, nil
}

func newPreflightStructuralDocument(in StructuralPreflightInput) preflightStructuralDocument {
	participants := make([]preflightParticipantDocument, 0, len(in.Participants))
	for _, participant := range in.Participants {
		participants = append(participants, preflightParticipantDocument{
			ParticipantID:        participant.ParticipantID.String(),
			PlayerID:             participant.PlayerID.String(),
			Seed:                 participant.Seed,
			Attendance:           string(participant.Attendance),
			ReservedTournamentID: participant.ReservedTournamentID.String(),
		})
	}
	categoryPools := make([]preflightCategoryPoolDocument, 0, len(in.CategoryPools))
	for _, pool := range in.CategoryPools {
		categories := make([]string, 0, len(pool.Categories))
		for _, category := range pool.Categories {
			categories = append(categories, string(category))
		}
		categoryPools = append(categoryPools, preflightCategoryPoolDocument{
			ID:         pool.ID.String(),
			Revision:   pool.Revision,
			Format:     string(pool.Format),
			Categories: categories,
		})
	}
	overrides := make([]preflightOverrideEvidenceDocument, 0, len(in.Overrides))
	for _, override := range in.Overrides {
		overrides = append(overrides, preflightOverrideEvidenceDocument{
			Pair:      newPreflightPairDocument(override.Pair),
			ActorID:   override.ActorID.String(),
			Confirmed: override.Confirmed,
			Reason:    override.Reason,
		})
	}
	return preflightStructuralDocument{
		TournamentID:       in.TournamentID.String(),
		Preset:             in.Preset.String(),
		ExpectedRosterSize: in.ExpectedRosterSize,
		Participants:       participants,
		CategoryPools:      categoryPools,
		Pairings:           newPreflightPairDocuments(in.Pairings),
		ByeParticipantID:   in.ByeParticipantID.String(),
		RepeatedPairings:   newPreflightPairDocuments(in.RepeatedPairings),
		Overrides:          overrides,
	}
}

func newPreflightPairDocuments(pairs []SwissPair) []preflightPairDocument {
	result := make([]preflightPairDocument, 0, len(pairs))
	for _, pair := range pairs {
		result = append(result, newPreflightPairDocument(pair))
	}
	return result
}

func newPreflightPairDocument(pair SwissPair) preflightPairDocument {
	return preflightPairDocument{
		FirstParticipantID:  pair.FirstParticipantID.String(),
		SecondParticipantID: pair.SecondParticipantID.String(),
	}
}

func newPreflightTaskHealthDocument(in TaskHealthPreflightInput) preflightTaskHealthInputDocument {
	versions := make([]preflightTaskVersionHealthDocument, 0, len(in.Versions))
	for _, version := range in.Versions {
		versions = append(versions, preflightTaskVersionHealthDocument{
			TaskID:          version.TaskID.String(),
			Version:         version.Version,
			PoolRevisionID:  version.PoolRevisionID.String(),
			PoolKind:        string(version.PoolKind),
			Exists:          version.Exists,
			Enabled:         version.Enabled,
			Healthy:         version.Healthy,
			MutationLocked:  version.MutationLocked,
			PubliclyExposed: version.PubliclyExposed,
		})
	}
	return preflightTaskHealthInputDocument{
		NormalPool: newPreflightTaskPoolDocument(in.NormalPool),
		GoldenPool: newPreflightTaskPoolDocument(in.GoldenPool),
		Versions:   versions,
	}
}

func newPreflightTaskPoolDocument(in TaskPoolRevision) preflightTaskPoolDocument {
	versions := make([]preflightTaskVersionDocument, 0, len(in.Versions))
	for _, version := range in.Versions {
		versions = append(versions, preflightTaskVersionDocument{
			TaskID:  version.TaskID.String(),
			Version: version.Version,
		})
	}
	return preflightTaskPoolDocument{
		ID:       in.ID.String(),
		Revision: in.Revision,
		Kind:     string(in.Kind),
		Versions: versions,
	}
}

func newPreflightRuntimeDocument(in RuntimePreflightInput) preflightRuntimeDocument {
	dependencies := make([]preflightDependencyDocument, 0, len(in.Dependencies))
	for _, dependency := range in.Dependencies {
		dependencies = append(dependencies, preflightDependencyDocument{
			Name:     string(dependency.Name),
			Healthy:  dependency.Healthy,
			Revision: dependency.Revision,
		})
	}
	return preflightRuntimeDocument{
		TournamentID:    in.TournamentID.String(),
		Preset:          in.Preset.String(),
		RosterSize:      in.RosterSize,
		ContentRevision: in.ContentRevision,
		Configuration: preflightRuntimeConfigurationDocument{
			Valid:    in.Configuration.Valid,
			Revision: in.Configuration.Revision,
		},
		Health: preflightRuntimeHealthDocument{
			AuthoritativeStorage: newPreflightRuntimeComponentDocument(in.Health.AuthoritativeStorage),
			Submission:           newPreflightRuntimeComponentDocument(in.Health.Submission),
			TaskDelivery:         newPreflightRuntimeComponentDocument(in.Health.TaskDelivery),
			Realtime:             newPreflightRuntimeComponentDocument(in.Health.Realtime),
		},
		Capacity: newPreflightCapacityDocument(in.Capacity),
		Clock: preflightClockDocument{
			ObservedAt:  canonicalPreflightTime(in.Clock.ObservedAt),
			ReferenceAt: canonicalPreflightTime(in.Clock.ReferenceAt),
			MaxSkew:     int64(in.Clock.MaxSkew),
		},
		Dependencies: dependencies,
		Schedule: preflightScheduleDocument{
			StartsAt:          canonicalPreflightTime(in.Schedule.StartsAt),
			MustFinishBy:      canonicalPreflightTime(in.Schedule.MustFinishBy),
			ProjectedDuration: int64(in.Schedule.ProjectedDuration),
		},
	}
}

func newPreflightRuntimeComponentDocument(in RuntimeComponentHealth) preflightRuntimeComponentDocument {
	return preflightRuntimeComponentDocument{Healthy: in.Healthy, Revision: in.Revision}
}

func newPreflightCapacityDocument(in *CapacityCertification) *preflightCapacityDocument {
	if in == nil {
		return nil
	}
	return &preflightCapacityDocument{
		ID:                   in.ID.String(),
		TournamentID:         in.TournamentID.String(),
		ContentRevision:      in.ContentRevision,
		RosterSize:           in.RosterSize,
		NormalPoolRevisionID: in.NormalPoolRevisionID.String(),
		NormalPoolRevision:   in.NormalPoolRevision,
		GoldenPoolRevisionID: in.GoldenPoolRevisionID.String(),
		GoldenPoolRevision:   in.GoldenPoolRevision,
		NormalProofDigest:    in.NormalProofDigest,
		GoldenProofDigest:    in.GoldenProofDigest,
		CertifiedAt:          canonicalPreflightTime(in.CertifiedAt),
	}
}

func canonicalPreflightTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}

func preflightSourceRevisions(in PreflightReportInput) []PreflightSourceRevision {
	result := []PreflightSourceRevision{
		{Source: "content", Value: strconv.FormatInt(in.Runtime.ContentRevision, 10)},
		{Source: "pairing", Value: strconv.FormatInt(in.PairingRevision, 10)},
		{Source: "roster", Value: strconv.FormatInt(in.RosterRevision, 10)},
		{Source: "runtime.configuration", Value: in.Runtime.Configuration.Revision},
		{Source: "runtime.authoritative_storage", Value: in.Runtime.Health.AuthoritativeStorage.Revision},
		{Source: "runtime.submission", Value: in.Runtime.Health.Submission.Revision},
		{Source: "runtime.task_delivery", Value: in.Runtime.Health.TaskDelivery.Revision},
		{Source: "runtime.realtime", Value: in.Runtime.Health.Realtime.Revision},
		{Source: "tasks.normal", Value: revisionIdentity(in.TaskHealth.NormalPool.ID, in.TaskHealth.NormalPool.Revision)},
		{Source: "tasks.golden", Value: revisionIdentity(in.TaskHealth.GoldenPool.ID, in.TaskHealth.GoldenPool.Revision)},
	}
	if in.Runtime.Capacity == nil {
		result = append(result, PreflightSourceRevision{Source: "capacity", Value: "missing"})
	} else {
		result = append(result, PreflightSourceRevision{Source: "capacity", Value: in.Runtime.Capacity.ID.String()})
	}
	for _, pool := range in.Structural.CategoryPools {
		result = append(result, PreflightSourceRevision{
			Source: "categories." + string(pool.Format),
			Value:  revisionIdentity(pool.ID, pool.Revision),
		})
	}
	for _, dependency := range in.Runtime.Dependencies {
		result = append(result, PreflightSourceRevision{
			Source: "dependency." + string(dependency.Name),
			Value:  dependency.Revision,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Value < result[j].Value
	})
	return uniquePreflightSourceRevisions(result)
}

func revisionIdentity(id uuid.UUID, revision int64) string {
	return id.String() + "@" + strconv.FormatInt(revision, 10)
}

func preflightProofHash(document preflightProofDocument) (string, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", preflightReportError("cannot encode proof document")
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validPreflightSourceRevisions(revisions []PreflightSourceRevision) bool {
	if len(revisions) == 0 {
		return false
	}
	for i, revision := range revisions {
		if !operatorSafeString(revision.Source) || !operatorSafeString(revision.Value) {
			return false
		}
		if i > 0 && !preflightSourceRevisionLess(revisions[i-1], revision) {
			return false
		}
	}
	return true
}

func validPreflightChecks(checks []ArenaPreflightCheck) bool {
	if len(checks) != len(preflightReportCheckCodes) {
		return false
	}
	for i, check := range checks {
		if check.Code != preflightReportCheckCodes[i] || !operatorSafeString(string(check.Code)) ||
			!operatorSafeString(check.Explanation) || !sortedUniqueOperatorStrings(check.Evidence, true) {
			return false
		}
		if check.Passed && len(check.Evidence) == 0 {
			return false
		}
	}
	return true
}

func sortedUniqueOperatorStrings(values []string, allowEmpty bool) bool {
	if len(values) == 0 {
		return allowEmpty
	}
	for i, value := range values {
		if !operatorSafeString(value) || i > 0 && values[i-1] >= value {
			return false
		}
	}
	return true
}

func operatorSafeString(value string) bool {
	return value != "" && strings.IndexFunc(value, unicode.IsControl) == -1
}

func appendClonedPreflightChecks(out []ArenaPreflightCheck, checks []ArenaPreflightCheck) []ArenaPreflightCheck {
	for _, check := range checks {
		check.Evidence = append([]string{}, check.Evidence...)
		out = append(out, check)
	}
	return out
}

func uniquePreflightSourceRevisions(revisions []PreflightSourceRevision) []PreflightSourceRevision {
	result := revisions[:0]
	for _, revision := range revisions {
		if len(result) == 0 || result[len(result)-1] != revision {
			result = append(result, revision)
		}
	}
	return result
}

func preflightSourceRevisionLess(first, second PreflightSourceRevision) bool {
	return first.Source < second.Source || first.Source == second.Source && first.Value < second.Value
}

func sortedUniqueStrings(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func normalizeSwissPairs(pairs []SwissPair) []SwissPair {
	result := append([]SwissPair{}, pairs...)
	for i := range result {
		result[i] = normalizeSwissPair(result[i])
	}
	sort.Slice(result, func(i, j int) bool {
		return swissPairLess(result[i], result[j])
	})
	return result
}

func normalizeSwissPair(pair SwissPair) SwissPair {
	if bytes.Compare(pair.FirstParticipantID[:], pair.SecondParticipantID[:]) > 0 {
		pair.FirstParticipantID, pair.SecondParticipantID = pair.SecondParticipantID, pair.FirstParticipantID
	}
	return pair
}

func swissPairLess(first, second SwissPair) bool {
	if comparison := bytes.Compare(first.FirstParticipantID[:], second.FirstParticipantID[:]); comparison != 0 {
		return comparison < 0
	}
	return bytes.Compare(first.SecondParticipantID[:], second.SecondParticipantID[:]) < 0
}

func structuralParticipantLess(first, second StructuralParticipant) bool {
	if comparison := bytes.Compare(first.ParticipantID[:], second.ParticipantID[:]); comparison != 0 {
		return comparison < 0
	}
	if comparison := bytes.Compare(first.PlayerID[:], second.PlayerID[:]); comparison != 0 {
		return comparison < 0
	}
	if first.Seed != second.Seed {
		return first.Seed < second.Seed
	}
	if first.Attendance != second.Attendance {
		return first.Attendance < second.Attendance
	}
	return bytes.Compare(first.ReservedTournamentID[:], second.ReservedTournamentID[:]) < 0
}

func structuralOverrideLess(first, second StructuralOverrideEvidence) bool {
	if first.Pair != second.Pair {
		return swissPairLess(first.Pair, second.Pair)
	}
	if comparison := bytes.Compare(first.ActorID[:], second.ActorID[:]); comparison != 0 {
		return comparison < 0
	}
	if first.Confirmed != second.Confirmed {
		return !first.Confirmed && second.Confirmed
	}
	return first.Reason < second.Reason
}

func taskVersionHealthLess(first, second TaskVersionHealth) bool {
	if comparison := bytes.Compare(first.TaskID[:], second.TaskID[:]); comparison != 0 {
		return comparison < 0
	}
	if first.Version != second.Version {
		return first.Version < second.Version
	}
	if comparison := bytes.Compare(first.PoolRevisionID[:], second.PoolRevisionID[:]); comparison != 0 {
		return comparison < 0
	}
	if first.PoolKind != second.PoolKind {
		return first.PoolKind < second.PoolKind
	}
	return taskVersionHealthFlags(first) < taskVersionHealthFlags(second)
}

func taskVersionHealthFlags(in TaskVersionHealth) string {
	flags := [...]bool{in.Exists, in.Enabled, in.Healthy, in.MutationLocked, in.PubliclyExposed}
	var result strings.Builder
	result.Grow(len(flags))
	for _, flag := range flags {
		if flag {
			result.WriteByte('1')
		} else {
			result.WriteByte('0')
		}
	}
	return result.String()
}

func safeRuntimeRevision(value string) string {
	if normalized, ok := normalizeRuntimeRevision(value); ok {
		return normalized
	}
	return "invalid#" + safeValueDigest(value)
}

func safeCapacityProofDigest(value string) string {
	if validCapacityDigest(value) {
		return strings.ToLower(value)
	}
	return "invalid#" + safeValueDigest(value)
}

func safeTypedValue(value string, valid bool) string {
	if valid {
		return value
	}
	return "invalid#" + safeValueDigest(value)
}

func safeValueDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func preflightReportError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidPreflightReport, message)
}
