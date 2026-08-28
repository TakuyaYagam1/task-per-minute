package arena_test

import (
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestPreflightReport(t *testing.T) {
	t.Parallel()

	t.Run("stores a complete immutable proof revision", func(t *testing.T) {
		t.Parallel()

		input := task022PreflightInput(t)
		report, err := arena.NewPreflightReportRevision(
			task021ID(80),
			time.Date(2026, time.August, 29, 1, 0, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewPreflightReportRevision() error = %v", err)
		}
		if err := report.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
		if !report.Passed() {
			t.Fatalf("valid composed report failed: %+v", report)
		}
		if report.AlgorithmVersion != arena.PreflightReportAlgorithmV1 ||
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
		corrupted.Checks = append([]arena.ArenaPreflightCheck(nil), report.Checks...)
		corrupted.Checks[0].Explanation = "corrupted"
		if err := corrupted.Validate(); !errors.Is(err, arena.ErrInvalidPreflightReport) {
			t.Fatalf("corrupted Validate() error = %v", err)
		}
	})

	t.Run("invalidates on every authoritative semantic change", func(t *testing.T) {
		t.Parallel()

		base := task022PreflightInput(t)
		report, err := arena.NewPreflightReportRevision(
			task021ID(81),
			time.Date(2026, time.August, 29, 1, 1, 0, 0, time.UTC),
			base,
		)
		if err != nil {
			t.Fatalf("NewPreflightReportRevision() error = %v", err)
		}

		tests := []struct {
			name   string
			mutate func(*arena.PreflightReportInput)
		}{
			{name: "roster revision", mutate: func(in *arena.PreflightReportInput) { in.RosterRevision++ }},
			{name: "pairing revision", mutate: func(in *arena.PreflightReportInput) { in.PairingRevision++ }},
			{name: "structural roster", mutate: func(in *arena.PreflightReportInput) {
				in.Structural.Participants[0].Attendance = domain.ArenaAttendanceStateWithdrawn
			}},
			{name: "category pools", mutate: func(in *arena.PreflightReportInput) {
				in.Structural.CategoryPools[0].Revision++
			}},
			{name: "pairings", mutate: func(in *arena.PreflightReportInput) {
				in.Structural.Pairings[0].SecondParticipantID = in.Structural.Pairings[1].SecondParticipantID
			}},
			{name: "task pool", mutate: func(in *arena.PreflightReportInput) { in.TaskHealth.NormalPool.Revision++ }},
			{name: "task health", mutate: func(in *arena.PreflightReportInput) {
				in.TaskHealth.Versions[0].Healthy = !in.TaskHealth.Versions[0].Healthy
			}},
			{name: "content revision", mutate: func(in *arena.PreflightReportInput) { in.Runtime.ContentRevision++ }},
			{name: "runtime configuration", mutate: func(in *arena.PreflightReportInput) { in.Runtime.Configuration.Revision = "runtime-v4" }},
			{name: "runtime component", mutate: func(in *arena.PreflightReportInput) { in.Runtime.Health.Submission.Healthy = false }},
			{name: "capacity", mutate: func(in *arena.PreflightReportInput) { in.Runtime.Capacity.ID = task021ID(84) }},
			{name: "clock", mutate: func(in *arena.PreflightReportInput) {
				in.Runtime.Clock.ReferenceAt = in.Runtime.Clock.ReferenceAt.Add(time.Millisecond)
			}},
			{name: "dependency", mutate: func(in *arena.PreflightReportInput) { in.Runtime.Dependencies[0].Revision = "postgres-v19" }},
			{name: "schedule", mutate: func(in *arena.PreflightReportInput) { in.Runtime.Schedule.ProjectedDuration += time.Second }},
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
		report, err := arena.NewPreflightReportRevision(
			task021ID(87),
			time.Date(2026, time.August, 29, 1, 4, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewPreflightReportRevision() error = %v", err)
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
		input.TaskHealth.NormalPool.ID = task021ID(88)
		report, err := arena.NewPreflightReportRevision(
			task021ID(89),
			time.Date(2026, time.August, 29, 1, 5, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewPreflightReportRevision() error = %v", err)
		}
		if report.Passed() {
			t.Fatal("report with task and capacity source mismatch passed")
		}
		composed := arena.ArenaPreflightReport{Checks: report.Checks}
		if preflightCheck(t, composed, arena.PreflightCodeRuntimeConfiguration).Passed {
			t.Fatal("source mismatch did not fail the composed runtime configuration check")
		}
	})

	t.Run("normalizes ordering and ignores private diagnostics", func(t *testing.T) {
		t.Parallel()

		input := task022PreflightInput(t)
		report, err := arena.NewPreflightReportRevision(
			task021ID(85),
			time.Date(2026, time.August, 29, 1, 2, 0, 0, time.UTC),
			input,
		)
		if err != nil {
			t.Fatalf("NewPreflightReportRevision() error = %v", err)
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

		other, err := arena.NewPreflightReportRevision(
			task021ID(86),
			time.Date(2026, time.August, 29, 1, 3, 0, 0, time.UTC),
			reordered,
		)
		if err != nil {
			t.Fatalf("NewPreflightReportRevision(reordered) error = %v", err)
		}
		if report.ProofHash != other.ProofHash || !reflect.DeepEqual(report.NormalizedInputs, other.NormalizedInputs) {
			t.Fatalf("normalized revisions differ: %s != %s", report.ProofHash, other.ProofHash)
		}
	})
}

func task022PreflightInput(t *testing.T) arena.PreflightReportInput {
	t.Helper()

	runtime := task021RuntimePreflightInput(t)
	normalCapacity := task021NormalCapacityInput(runtime.RosterSize)
	goldenCapacity := task021GoldenCapacityInput(runtime.RosterSize)
	participants := make([]arena.StructuralParticipant, runtime.RosterSize)
	pairings := make([]arena.SwissPair, 0, runtime.RosterSize/2)
	for i := range participants {
		participants[i] = arena.StructuralParticipant{
			ParticipantID:        task021ID(70 + i),
			PlayerID:             task021ID(90 + i),
			Seed:                 i + 1,
			Attendance:           domain.ArenaAttendanceStateCheckedIn,
			ReservedTournamentID: runtime.TournamentID,
		}
		if i%2 == 1 {
			pairings = append(pairings, arena.SwissPair{
				FirstParticipantID:  participants[i-1].ParticipantID,
				SecondParticipantID: participants[i].ParticipantID,
			})
		}
	}
	taskHealth := arena.TaskHealthPreflightInput{
		NormalPool: normalCapacity.NormalPool,
		GoldenPool: goldenCapacity.GoldenPool,
	}
	taskHealth.Versions = appendTaskVersionHealth(taskHealth.Versions, taskHealth.NormalPool)
	taskHealth.Versions = appendTaskVersionHealth(taskHealth.Versions, taskHealth.GoldenPool)
	taskHealth.Versions[0].InternalHealthDetail = "private-task-marker"
	return arena.PreflightReportInput{
		RosterRevision:  7,
		PairingRevision: 4,
		Structural: arena.StructuralPreflightInput{
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

func cloneTask022PreflightInput(input arena.PreflightReportInput) arena.PreflightReportInput {
	clone := input
	clone.Structural.Participants = append([]arena.StructuralParticipant(nil), input.Structural.Participants...)
	clone.Structural.CategoryPools = append([]arena.CategoryPoolRevision(nil), input.Structural.CategoryPools...)
	for i := range clone.Structural.CategoryPools {
		clone.Structural.CategoryPools[i].Categories = append(
			[]domain.Category(nil), input.Structural.CategoryPools[i].Categories...,
		)
	}
	clone.Structural.Pairings = append([]arena.SwissPair(nil), input.Structural.Pairings...)
	clone.Structural.RepeatedPairings = append([]arena.SwissPair(nil), input.Structural.RepeatedPairings...)
	clone.Structural.Overrides = append([]arena.StructuralOverrideEvidence(nil), input.Structural.Overrides...)
	clone.TaskHealth.NormalPool.Versions = append([]arena.TaskVersionRef(nil), input.TaskHealth.NormalPool.Versions...)
	clone.TaskHealth.GoldenPool.Versions = append([]arena.TaskVersionRef(nil), input.TaskHealth.GoldenPool.Versions...)
	clone.TaskHealth.Versions = append([]arena.TaskVersionHealth(nil), input.TaskHealth.Versions...)
	clone.Runtime = cloneRuntimePreflightInput(input.Runtime)
	return clone
}
