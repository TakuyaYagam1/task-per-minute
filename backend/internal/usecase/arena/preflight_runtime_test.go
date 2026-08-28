package arena_test

import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestPreflightRuntime(t *testing.T) {
	t.Parallel()

	wantCodes := []arena.ArenaPreflightCode{
		arena.PreflightCodeRuntimeConfiguration,
		arena.PreflightCodeRuntimeStorage,
		arena.PreflightCodeRuntimeSubmission,
		arena.PreflightCodeRuntimeTaskDelivery,
		arena.PreflightCodeRuntimeRealtime,
		arena.PreflightCodeRuntimeCapacity,
		arena.PreflightCodeRuntimeClock,
		arena.PreflightCodeRuntimeDependencies,
		arena.PreflightCodeRuntimeSchedule,
	}

	t.Run("emits a stable healthy report bound to the certified revisions", func(t *testing.T) {
		t.Parallel()

		input := task021RuntimePreflightInput(t)
		report := arena.EvaluateRuntimePreflight(input)
		if !report.Passed() {
			t.Fatalf("valid report failed: %+v", report)
		}
		if got := task021PreflightCodes(report); !reflect.DeepEqual(got, wantCodes) {
			t.Fatalf("codes = %v, want %v", got, wantCodes)
		}
		for _, check := range report.Checks {
			if check.Explanation == "" || len(check.Evidence) == 0 || !sort.StringsAreSorted(check.Evidence) {
				t.Fatalf("check is not stable and reviewable: %+v", check)
			}
		}
		serialized := strings.ToLower(task021FlattenPreflight(report))
		if strings.Contains(serialized, "private-storage-error") || strings.Contains(serialized, "private-realtime-error") {
			t.Fatalf("runtime report exposed internal health detail: %s", serialized)
		}

		reordered := cloneRuntimePreflightInput(input)
		slices.Reverse(reordered.Dependencies)
		if other := arena.EvaluateRuntimePreflight(reordered); !reflect.DeepEqual(other, report) {
			t.Fatalf("reordered report = %+v, want %+v", other, report)
		}
	})

	t.Run("reports every runtime gate and blocks a 16-player start without capacity", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			code   arena.ArenaPreflightCode
			mutate func(*arena.RuntimePreflightInput)
		}{
			{name: "configuration", code: arena.PreflightCodeRuntimeConfiguration, mutate: func(in *arena.RuntimePreflightInput) {
				in.Configuration.Valid = false
			}},
			{name: "storage", code: arena.PreflightCodeRuntimeStorage, mutate: func(in *arena.RuntimePreflightInput) {
				in.Health.AuthoritativeStorage.Healthy = false
			}},
			{name: "submission", code: arena.PreflightCodeRuntimeSubmission, mutate: func(in *arena.RuntimePreflightInput) {
				in.Health.Submission.Healthy = false
			}},
			{name: "task delivery", code: arena.PreflightCodeRuntimeTaskDelivery, mutate: func(in *arena.RuntimePreflightInput) {
				in.Health.TaskDelivery.Healthy = false
			}},
			{name: "realtime", code: arena.PreflightCodeRuntimeRealtime, mutate: func(in *arena.RuntimePreflightInput) {
				in.Health.Realtime.Healthy = false
			}},
			{name: "capacity", code: arena.PreflightCodeRuntimeCapacity, mutate: func(in *arena.RuntimePreflightInput) {
				in.Capacity = nil
			}},
			{name: "clock", code: arena.PreflightCodeRuntimeClock, mutate: func(in *arena.RuntimePreflightInput) {
				in.Clock.ObservedAt = in.Clock.ReferenceAt.Add(3 * time.Second)
			}},
			{name: "dependency", code: arena.PreflightCodeRuntimeDependencies, mutate: func(in *arena.RuntimePreflightInput) {
				in.Dependencies[1].Healthy = false
			}},
			{name: "schedule", code: arena.PreflightCodeRuntimeSchedule, mutate: func(in *arena.RuntimePreflightInput) {
				in.Schedule.ProjectedDuration = 61 * time.Minute
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				input := task021RuntimePreflightInput(t)
				test.mutate(&input)
				report := arena.EvaluateRuntimePreflight(input)
				if report.Passed() {
					t.Fatalf("mutated report passed: %+v", report)
				}
				failed := task021FailedPreflightCodes(report)
				if !slices.Contains(failed, test.code) {
					t.Fatalf("failed codes = %v, want %s", failed, test.code)
				}
			})
		}
	})

	t.Run("rejects a stale capacity certification", func(t *testing.T) {
		t.Parallel()

		input := task021RuntimePreflightInput(t)
		input.ContentRevision++
		report := arena.EvaluateRuntimePreflight(input)
		if !slices.Contains(task021FailedPreflightCodes(report), arena.PreflightCodeRuntimeCapacity) {
			t.Fatalf("stale capacity report = %+v", report)
		}
	})

	t.Run("rejects control characters in public revision evidence", func(t *testing.T) {
		t.Parallel()

		input := task021RuntimePreflightInput(t)
		input.Configuration.Revision = "runtime-v3\nprivate-runtime-marker"
		report := arena.EvaluateRuntimePreflight(input)
		if !slices.Contains(task021FailedPreflightCodes(report), arena.PreflightCodeRuntimeConfiguration) {
			t.Fatalf("unsafe revision report = %+v", report)
		}
		if strings.Contains(task021FlattenPreflight(report), "private-runtime-marker") {
			t.Fatalf("unsafe revision reached public evidence: %+v", report)
		}
	})
}

func task021RuntimePreflightInput(t *testing.T) arena.RuntimePreflightInput {
	t.Helper()
	normal := arena.ProveNormalCapacity(task021NormalCapacityInput(16))
	golden := arena.ProveGoldenCapacity(task021GoldenCapacityInput(16))
	tournamentID := task021ID(50)
	certifiedAt := time.Date(2026, time.August, 29, 0, 30, 0, 0, time.UTC)
	capacity, err := arena.NewCapacityCertification(
		task021ID(51), tournamentID, 8, normal, golden, certifiedAt,
	)
	if err != nil {
		t.Fatalf("NewCapacityCertification() error = %v", err)
	}
	start := certifiedAt.Add(30 * time.Minute)
	return arena.RuntimePreflightInput{
		TournamentID:    tournamentID,
		Preset:          domain.ArenaPresetV1,
		RosterSize:      16,
		ContentRevision: 8,
		Configuration: arena.RuntimeConfigurationHealth{
			Valid: true, Revision: "runtime-v3",
		},
		Health: arena.RuntimeHealth{
			AuthoritativeStorage: arena.RuntimeComponentHealth{Healthy: true, Revision: "storage-v4", InternalDetail: "private-storage-error"},
			Submission:           arena.RuntimeComponentHealth{Healthy: true, Revision: "submission-v2"},
			TaskDelivery:         arena.RuntimeComponentHealth{Healthy: true, Revision: "delivery-v5"},
			Realtime:             arena.RuntimeComponentHealth{Healthy: true, Revision: "realtime-v6", InternalDetail: "private-realtime-error"},
		},
		Capacity: &capacity,
		Clock: arena.RuntimeClockHealth{
			ObservedAt: certifiedAt.Add(time.Second), ReferenceAt: certifiedAt, MaxSkew: 2 * time.Second,
		},
		Dependencies: []arena.RuntimeDependencyHealth{
			{Name: arena.RuntimeDependencyPostgres, Healthy: true, Revision: "postgres-v18"},
			{Name: arena.RuntimeDependencyRedis, Healthy: true, Revision: "redis-v8"},
			{Name: arena.RuntimeDependencyObjectStorage, Healthy: true, Revision: "seaweedfs-v3"},
		},
		Schedule: arena.RuntimeScheduleHealth{
			StartsAt: start, MustFinishBy: start.Add(time.Hour), ProjectedDuration: 30 * time.Minute,
		},
	}
}

func cloneRuntimePreflightInput(input arena.RuntimePreflightInput) arena.RuntimePreflightInput {
	clone := input
	clone.Dependencies = append([]arena.RuntimeDependencyHealth(nil), input.Dependencies...)
	if input.Capacity != nil {
		capacity := *input.Capacity
		clone.Capacity = &capacity
	}
	return clone
}

func task021PreflightCodes(report arena.ArenaPreflightReport) []arena.ArenaPreflightCode {
	codes := make([]arena.ArenaPreflightCode, len(report.Checks))
	for i, check := range report.Checks {
		codes[i] = check.Code
	}
	return codes
}

func task021FailedPreflightCodes(report arena.ArenaPreflightReport) []arena.ArenaPreflightCode {
	codes := make([]arena.ArenaPreflightCode, 0)
	for _, check := range report.Checks {
		if !check.Passed {
			codes = append(codes, check.Code)
		}
	}
	return codes
}

func task021FlattenPreflight(report arena.ArenaPreflightReport) string {
	var output strings.Builder
	for _, check := range report.Checks {
		output.WriteString(string(check.Code))
		output.WriteString(check.Explanation)
		output.WriteString(strings.Join(check.Evidence, ","))
	}
	return output.String()
}
