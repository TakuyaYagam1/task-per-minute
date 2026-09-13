package preflight

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

const (
	ReportAlgorithmV1 = "tournament-preflight-report-v1"
	ReportAlgorithmV2 = "tournament-preflight-report-v2"
)

var ErrInvalidReport = errors.New("invalid preflight report")

type SourceRevision struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

type ReportInput struct {
	RosterRevision  int64
	PairingRevision int64
	Structural      StructuralInput
	TaskHealth      TaskHealthInput
	Runtime         RuntimeInput
}

type ReportRevision struct {
	ID               uuid.UUID        `json:"id"`
	TournamentID     uuid.UUID        `json:"tournament_id"`
	AlgorithmVersion string           `json:"algorithm_version"`
	EvaluatedAt      time.Time        `json:"evaluated_at"`
	NormalizedInputs []string         `json:"normalized_inputs"`
	Revisions        []SourceRevision `json:"revisions"`
	ProofHash        string           `json:"proof_hash"`
	Checks           []Check          `json:"checks"`
}

type preflightProofDocument struct {
	AlgorithmVersion string                `json:"algorithm_version"`
	TournamentID     string                `json:"tournament_id"`
	NormalizedInputs []string              `json:"normalized_inputs"`
	Revisions        []SourceRevision      `json:"revisions"`
	Checks           []preflightProofCheck `json:"checks"`
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

var preflightReportCheckCodes = []Code{
	CodeRosterComplete,
	CodeAttendance,
	CodeParticipantExclusive,
	CodePreset,
	CodeCategories,
	CodePairings,
	CodeByes,
	CodeOverrides,
	CodeTaskPoolsValid,
	CodeTaskInventory,
	CodeTaskMissing,
	CodeTaskDisabled,
	CodeTaskUnhealthy,
	CodeTaskMutable,
	CodeTaskExposed,
	CodeTaskWrongPool,
	CodeRuntimeConfiguration,
	CodeRuntimeStorage,
	CodeRuntimeSubmission,
	CodeRuntimeTaskDelivery,
	CodeRuntimeRealtime,
	CodeRuntimeCapacity,
	CodeRuntimeClock,
	CodeRuntimeDependencies,
	CodeRuntimeSchedule,
}

func NewReportRevision(
	id uuid.UUID,
	evaluatedAt time.Time,
	in ReportInput,
) (ReportRevision, error) {
	if id == uuid.Nil || !validRuntimeTime(evaluatedAt) || in.RosterRevision < 1 || in.PairingRevision < 1 {
		return ReportRevision{}, preflightReportError("missing identity, UTC timestamp, or source revision")
	}

	report, err := composePreflightReportRevision(ReportAlgorithmV2, id, evaluatedAt, in)
	if err != nil {
		return ReportRevision{}, err
	}
	if err := report.Validate(); err != nil {
		return ReportRevision{}, err
	}
	return report, nil
}

func (r ReportRevision) Passed() bool {
	return Report{Checks: r.Checks}.Passed()
}

func (r ReportRevision) InvalidatedBy(in ReportInput) bool {
	if r.Validate() != nil {
		return true
	}
	candidate, err := composePreflightReportRevision(r.AlgorithmVersion, r.ID, r.EvaluatedAt, in)
	return err != nil || candidate.ProofHash != r.ProofHash
}

func (r ReportRevision) Validate() error {
	if r.ID == uuid.Nil || r.TournamentID == uuid.Nil || !validRuntimeTime(r.EvaluatedAt) ||
		!validReportAlgorithmVersion(r.AlgorithmVersion) || !capacity.ValidProofDigest(r.ProofHash) {
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
	algorithmVersion string,
	id uuid.UUID,
	evaluatedAt time.Time,
	in ReportInput,
) (ReportRevision, error) {
	if !validReportAlgorithmVersion(algorithmVersion) {
		return ReportRevision{}, preflightReportError("unsupported report algorithm version")
	}
	if in.RosterRevision < 1 || in.PairingRevision < 1 {
		return ReportRevision{}, preflightReportError("source revisions must be positive")
	}
	normalized := normalizePreflightReportInput(in)
	tournamentID := normalized.Structural.TournamentID
	if tournamentID == uuid.Nil {
		tournamentID = normalized.Runtime.TournamentID
	}
	if tournamentID == uuid.Nil {
		return ReportRevision{}, preflightReportError("missing tournament identity")
	}

	structural := EvaluateStructure(normalized.Structural)
	tasks := EvaluateTaskHealth(normalized.TaskHealth)
	runtime := EvaluateRuntime(normalized.Runtime)
	if !preflightSourcesAligned(normalized) {
		runtime.Checks[0].Passed = false
		runtime.Checks[0].Evidence = append(runtime.Checks[0].Evidence, "source_alignment:mismatch")
		runtime.Checks[0].Evidence = sortedUniqueStrings(runtime.Checks[0].Evidence)
	}
	checks := make([]Check, 0, len(structural.Checks)+len(tasks.Checks)+len(runtime.Checks))
	checks = appendClonedPreflightChecks(checks, structural.Checks)
	checks = appendClonedPreflightChecks(checks, tasks.Checks)
	checks = appendClonedPreflightChecks(checks, runtime.Checks)

	normalizedInputs, err := preflightNormalizedInputs(normalized)
	if err != nil {
		return ReportRevision{}, err
	}
	revisions := preflightSourceRevisions(normalized)
	document := newPreflightProofDocument(
		algorithmVersion,
		tournamentID,
		normalizedInputs,
		revisions,
		checks,
	)
	proofHash, err := preflightProofHash(document)
	if err != nil {
		return ReportRevision{}, err
	}
	return ReportRevision{
		ID:               id,
		TournamentID:     tournamentID,
		AlgorithmVersion: algorithmVersion,
		EvaluatedAt:      evaluatedAt,
		NormalizedInputs: normalizedInputs,
		Revisions:        revisions,
		ProofHash:        proofHash,
		Checks:           checks,
	}, nil
}

func validReportAlgorithmVersion(version string) bool {
	return version == ReportAlgorithmV1 || version == ReportAlgorithmV2
}

func preflightSourcesAligned(in ReportInput) bool {
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
