package preflight_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestPreflightReport(t *testing.T) {
	t.Parallel()

	t.Run("stores a complete immutable proof revision", func(t *testing.T) {
		t.Parallel()

		input := task022PreflightInput(t)
		report, err := tournamentusecase.NewReportRevision(
			preflightCapacityID(80),
			time.Date(2026, time.August, 29, 1, 0, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewReportRevision() error = %v", err)
		}
		if err := report.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
		if !report.Passed() {
			t.Fatalf("valid composed report failed: %+v", report)
		}
		if report.AlgorithmVersion != tournamentusecase.ReportAlgorithmV2 ||
			len(report.ProofHash) != 64 || len(report.Checks) != 25 ||
			len(report.NormalizedInputs) == 0 || len(report.Revisions) == 0 {
			t.Fatalf("incomplete report revision: %+v", report)
		}
		if !sort.StringsAreSorted(report.NormalizedInputs) {
			t.Fatalf("normalized inputs are not sorted: %v", report.NormalizedInputs)
		}
		for _, check := range report.Checks {
			if check.Code == "" || check.Explanation == "" || len(check.Evidence) == 0 {
				t.Fatalf("operator check is incomplete: %+v", check)
			}
		}
		serialized := strings.ToLower(strings.Join(report.NormalizedInputs, "\n"))
		if strings.Contains(serialized, "private-runtime-marker") || strings.Contains(serialized, "private-task-marker") {
			t.Fatalf("private health detail reached durable input evidence: %s", serialized)
		}
		if report.InvalidatedBy(input) {
			t.Fatal("unchanged input invalidated the report")
		}

		corrupted := report
		corrupted.Checks = append([]tournamentusecase.Check(nil), report.Checks...)
		corrupted.Checks[0].Explanation = "corrupted"
		if err := corrupted.Validate(); !errors.Is(err, tournamentusecase.ErrInvalidReport) {
			t.Fatalf("corrupted Validate() error = %v", err)
		}
	})

	t.Run("invalidates on every authoritative semantic change", func(t *testing.T) {
		t.Parallel()

		base := task022PreflightInput(t)
		report, err := tournamentusecase.NewReportRevision(
			preflightCapacityID(81),
			time.Date(2026, time.August, 29, 1, 1, 0, 0, time.UTC),
			base,
		)
		if err != nil {
			t.Fatalf("NewReportRevision() error = %v", err)
		}

		tests := []struct {
			name   string
			mutate func(*tournamentusecase.ReportInput)
		}{
			{name: "roster revision", mutate: func(in *tournamentusecase.ReportInput) { in.RosterRevision++ }},
			{name: "pairing revision", mutate: func(in *tournamentusecase.ReportInput) { in.PairingRevision++ }},
			{name: "structural roster", mutate: func(in *tournamentusecase.ReportInput) {
				in.Structural.Participants[0].Attendance = domain.AttendanceStateWithdrawn
			}},
			{name: "category pools", mutate: func(in *tournamentusecase.ReportInput) {
				in.Structural.CategoryPools[0].Revision++
			}},
			{name: "pairings", mutate: func(in *tournamentusecase.ReportInput) {
				in.Structural.Pairings[0].SecondParticipantID = in.Structural.Pairings[1].SecondParticipantID
			}},
			{name: "task pool", mutate: func(in *tournamentusecase.ReportInput) { in.TaskHealth.NormalPool.Revision++ }},
			{name: "task health", mutate: func(in *tournamentusecase.ReportInput) {
				in.TaskHealth.Versions[0].Healthy = !in.TaskHealth.Versions[0].Healthy
			}},
			{name: "content revision", mutate: func(in *tournamentusecase.ReportInput) { in.Runtime.ContentRevision++ }},
			{name: "runtime configuration", mutate: func(in *tournamentusecase.ReportInput) { in.Runtime.Configuration.Revision = "runtime-v4" }},
			{name: "runtime component", mutate: func(in *tournamentusecase.ReportInput) { in.Runtime.Health.Submission.Healthy = false }},
			{name: "capacity", mutate: func(in *tournamentusecase.ReportInput) { in.Runtime.Capacity.ID = preflightCapacityID(84) }},
			{name: "clock", mutate: func(in *tournamentusecase.ReportInput) {
				in.Runtime.Clock.ReferenceAt = in.Runtime.Clock.ReferenceAt.Add(time.Millisecond)
			}},
			{name: "dependency", mutate: func(in *tournamentusecase.ReportInput) { in.Runtime.Dependencies[0].Revision = "postgres-v19" }},
			{name: "schedule", mutate: func(in *tournamentusecase.ReportInput) { in.Runtime.Schedule.ProjectedDuration += time.Second }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				input := cloneTask022PreflightInput(base)
				test.mutate(&input)
				if !report.InvalidatedBy(input) {
					t.Fatalf("%s did not invalidate proof %s", test.name, report.ProofHash)
				}
			})
		}
	})

	t.Run("redacts malformed durable source values", func(t *testing.T) {
		t.Parallel()

		input := task022PreflightInput(t)
		input.Runtime.Capacity.NormalProofDigest = "private-capacity-marker\n"
		report, err := tournamentusecase.NewReportRevision(
			preflightCapacityID(87),
			time.Date(2026, time.August, 29, 1, 4, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewReportRevision() error = %v", err)
		}
		if report.Passed() {
			t.Fatal("report with a malformed capacity proof passed")
		}
		if strings.Contains(strings.Join(report.NormalizedInputs, "\n"), "private-capacity-marker") {
			t.Fatal("malformed capacity proof reached durable evidence")
		}
	})

	t.Run("blocks a report composed from different source revisions", func(t *testing.T) {
		t.Parallel()

		input := task022PreflightInput(t)
		input.TaskHealth.NormalPool.ID = preflightCapacityID(88)
		report, err := tournamentusecase.NewReportRevision(
			preflightCapacityID(89),
			time.Date(2026, time.August, 29, 1, 5, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewReportRevision() error = %v", err)
		}
		if report.Passed() {
			t.Fatal("report with task and capacity source mismatch passed")
		}
		composed := tournamentusecase.Report{Checks: report.Checks}
		if preflightCheck(t, composed, tournamentusecase.CodeRuntimeConfiguration).Passed {
			t.Fatal("source mismatch did not fail the composed runtime configuration check")
		}
	})

	t.Run("normalizes ordering and ignores private diagnostics", func(t *testing.T) {
		t.Parallel()

		input := task022PreflightInput(t)
		report, err := tournamentusecase.NewReportRevision(
			preflightCapacityID(85),
			time.Date(2026, time.August, 29, 1, 2, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewReportRevision() error = %v", err)
		}

		reordered := cloneTask022PreflightInput(input)
		slices.Reverse(reordered.Structural.Participants)
		slices.Reverse(reordered.Structural.CategoryPools)
		for i := range reordered.Structural.CategoryPools {
			slices.Reverse(reordered.Structural.CategoryPools[i].Categories)
		}
		slices.Reverse(reordered.Structural.Pairings)
		slices.Reverse(reordered.TaskHealth.NormalPool.Versions)
		slices.Reverse(reordered.TaskHealth.GoldenPool.Versions)
		slices.Reverse(reordered.Runtime.Dependencies)
		slices.Reverse(reordered.TaskHealth.Versions)
		reordered.Runtime.Health.AuthoritativeStorage.InternalDetail = "different-private-runtime-marker"
		reordered.TaskHealth.Versions[0].InternalHealthDetail = "different-private-task-marker"
		if report.InvalidatedBy(reordered) {
			t.Fatal("ordering or private diagnostic detail invalidated the report")
		}

		other, err := tournamentusecase.NewReportRevision(
			preflightCapacityID(86),
			time.Date(2026, time.August, 29, 1, 3, 0, 0, time.UTC),
			reordered,
		)
		if err != nil {
			t.Fatalf("NewReportRevision(reordered) error = %v", err)
		}
		if report.ProofHash != other.ProofHash || !reflect.DeepEqual(report.NormalizedInputs, other.NormalizedInputs) {
			t.Fatalf("normalized revisions differ: %s != %s", report.ProofHash, other.ProofHash)
		}
	})

	t.Run("continues to validate historical V1 evidence", func(t *testing.T) {
		t.Parallel()

		input := task022PreflightInput(t)
		report, err := tournamentusecase.NewReportRevision(
			preflightCapacityID(90),
			time.Date(2026, time.August, 29, 1, 6, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewReportRevision() error = %v", err)
		}
		report.AlgorithmVersion = tournamentusecase.ReportAlgorithmV1
		report.ProofHash = reportProofHash(report)

		if err := report.Validate(); err != nil {
			t.Fatalf("historical V1 Validate() error = %v", err)
		}
		if !report.Passed() || report.InvalidatedBy(input) {
			t.Fatalf("historical V1 report lost replay semantics: passed=%v invalidated=%v", report.Passed(), report.InvalidatedBy(input))
		}
	})
}

func reportProofHash(report tournamentusecase.ReportRevision) string {
	type proofCheck struct {
		Code        string   `json:"code"`
		Passed      bool     `json:"passed"`
		Explanation string   `json:"explanation"`
		Evidence    []string `json:"evidence"`
	}
	type proofDocument struct {
		AlgorithmVersion string                             `json:"algorithm_version"`
		TournamentID     string                             `json:"tournament_id"`
		NormalizedInputs []string                           `json:"normalized_inputs"`
		Revisions        []tournamentusecase.SourceRevision `json:"revisions"`
		Checks           []proofCheck                       `json:"checks"`
	}
	checks := make([]proofCheck, len(report.Checks))
	for index, check := range report.Checks {
		checks[index] = proofCheck{
			Code: string(check.Code), Passed: check.Passed, Explanation: check.Explanation,
			Evidence: append([]string(nil), check.Evidence...),
		}
	}
	document := proofDocument{
		AlgorithmVersion: report.AlgorithmVersion,
		TournamentID:     report.TournamentID.String(),
		NormalizedInputs: append([]string(nil), report.NormalizedInputs...),
		Revisions:        append([]tournamentusecase.SourceRevision(nil), report.Revisions...),
		Checks:           checks,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func task022PreflightInput(t *testing.T) tournamentusecase.ReportInput {
	t.Helper()

	runtime := task021RuntimePreflightInput(t)
	normalCapacity := preflightNormalCapacityInput(runtime.RosterSize)
	goldenCapacity := preflightGoldenCapacityInput(runtime.RosterSize)
	participants := make([]tournamentusecase.Participant, runtime.RosterSize)
	pairings := make([]swissusecase.Pair, 0, runtime.RosterSize/2)
	for i := range participants {
		participants[i] = tournamentusecase.Participant{
			ParticipantID:        preflightCapacityID(70 + i),
			PlayerID:             preflightCapacityID(90 + i),
			Seed:                 i + 1,
			Attendance:           domain.AttendanceStateCheckedIn,
			ReservedTournamentID: runtime.TournamentID,
		}
		if i%2 == 1 {
			pairings = append(pairings, swissusecase.Pair{
				FirstParticipantID:  participants[i-1].ParticipantID,
				SecondParticipantID: participants[i].ParticipantID,
			})
		}
	}
	taskHealth := tournamentusecase.TaskHealthInput{
		NormalPool: normalCapacity.NormalPool,
		GoldenPool: goldenCapacity.GoldenPool,
	}
	taskHealth.Versions = appendTaskVersionHealth(taskHealth.Versions, taskHealth.NormalPool)
	taskHealth.Versions = appendTaskVersionHealth(taskHealth.Versions, taskHealth.GoldenPool)
	taskHealth.Versions[0].InternalHealthDetail = "private-task-marker"
	return tournamentusecase.ReportInput{
		RosterRevision:  7,
		PairingRevision: 4,
		Structural: tournamentusecase.StructuralInput{
			TournamentID:       runtime.TournamentID,
			Preset:             runtime.Preset,
			ExpectedRosterSize: runtime.RosterSize,
			Participants:       participants,
			CategoryPools:      normalCapacity.CategoryPools,
			Pairings:           pairings,
		},
		TaskHealth: taskHealth,
		Runtime:    runtime,
	}
}

func cloneTask022PreflightInput(input tournamentusecase.ReportInput) tournamentusecase.ReportInput {
	clone := input
	clone.Structural.Participants = append([]tournamentusecase.Participant(nil), input.Structural.Participants...)
	clone.Structural.CategoryPools = append([]domain.CategoryPoolRevision(nil), input.Structural.CategoryPools...)
	for i := range clone.Structural.CategoryPools {
		clone.Structural.CategoryPools[i].Categories = append(
			[]domain.Category(nil), input.Structural.CategoryPools[i].Categories...,
		)
	}
	clone.Structural.Pairings = append([]swissusecase.Pair(nil), input.Structural.Pairings...)
	clone.Structural.RepeatedPairings = append([]swissusecase.Pair(nil), input.Structural.RepeatedPairings...)
	clone.Structural.Overrides = append([]tournamentusecase.OverrideEvidence(nil), input.Structural.Overrides...)
	clone.TaskHealth.NormalPool.Versions = append([]domain.TaskVersionRef(nil), input.TaskHealth.NormalPool.Versions...)
	clone.TaskHealth.GoldenPool.Versions = append([]domain.TaskVersionRef(nil), input.TaskHealth.GoldenPool.Versions...)
	clone.TaskHealth.Versions = append([]domain.TaskVersionHealth(nil), input.TaskHealth.Versions...)
	clone.Runtime = cloneRuntimePreflightInput(input.Runtime)
	return clone
}
