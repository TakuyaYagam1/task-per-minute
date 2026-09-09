package preflight_test

import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	tournamentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
	"github.com/stretchr/testify/require"
)

func TestPreflightRuntime(t *testing.T) {
	t.Parallel()

	wantCodes := []tournamentusecase.Code{
		tournamentusecase.CodeRuntimeConfiguration,
		tournamentusecase.CodeRuntimeStorage,
		tournamentusecase.CodeRuntimeSubmission,
		tournamentusecase.CodeRuntimeTaskDelivery,
		tournamentusecase.CodeRuntimeRealtime,
		tournamentusecase.CodeRuntimeCapacity,
		tournamentusecase.CodeRuntimeClock,
		tournamentusecase.CodeRuntimeDependencies,
		tournamentusecase.CodeRuntimeSchedule,
	}

	t.Run("emits a stable healthy report bound to the certified revisions", func(t *testing.T) {
		t.Parallel()

		input := task021RuntimePreflightInput(t)
		report := tournamentusecase.EvaluateRuntime(input)
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
		if other := tournamentusecase.EvaluateRuntime(reordered); !reflect.DeepEqual(other, report) {
			t.Fatalf("reordered report = %+v, want %+v", other, report)
		}
	})

	t.Run("reports every runtime gate and blocks a 16-player start without capacity", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			code   tournamentusecase.Code
			mutate func(*tournamentusecase.RuntimeInput)
		}{
			{name: "configuration", code: tournamentusecase.CodeRuntimeConfiguration, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Configuration.Valid = false
			}},
			{name: "storage", code: tournamentusecase.CodeRuntimeStorage, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Health.AuthoritativeStorage.Healthy = false
			}},
			{name: "submission", code: tournamentusecase.CodeRuntimeSubmission, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Health.Submission.Healthy = false
			}},
			{name: "task delivery", code: tournamentusecase.CodeRuntimeTaskDelivery, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Health.TaskDelivery.Healthy = false
			}},
			{name: "realtime", code: tournamentusecase.CodeRuntimeRealtime, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Health.Realtime.Healthy = false
			}},
			{name: "capacity", code: tournamentusecase.CodeRuntimeCapacity, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Capacity = nil
			}},
			{name: "clock", code: tournamentusecase.CodeRuntimeClock, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Clock.ObservedAt = in.Clock.ReferenceAt.Add(3 * time.Second)
			}},
			{name: "dependency", code: tournamentusecase.CodeRuntimeDependencies, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Dependencies[1].Healthy = false
			}},
			{name: "schedule", code: tournamentusecase.CodeRuntimeSchedule, mutate: func(in *tournamentusecase.RuntimeInput) {
				in.Schedule.ProjectedDuration = 61 * time.Minute
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				input := task021RuntimePreflightInput(t)
				test.mutate(&input)
				report := tournamentusecase.EvaluateRuntime(input)
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
		report := tournamentusecase.EvaluateRuntime(input)
		if !slices.Contains(task021FailedPreflightCodes(report), tournamentusecase.CodeRuntimeCapacity) {
			t.Fatalf("stale capacity report = %+v", report)
		}
	})

	t.Run("rejects control characters in public revision evidence", func(t *testing.T) {
		t.Parallel()

		input := task021RuntimePreflightInput(t)
		input.Configuration.Revision = "runtime-v3\nprivate-runtime-marker"
		report := tournamentusecase.EvaluateRuntime(input)
		if !slices.Contains(task021FailedPreflightCodes(report), tournamentusecase.CodeRuntimeConfiguration) {
			t.Fatalf("unsafe revision report = %+v", report)
		}
		if strings.Contains(task021FlattenPreflight(report), "private-runtime-marker") {
			t.Fatalf("unsafe revision reached public evidence: %+v", report)
		}
	})
}

func TestApplyRuntimeHealthPreservesDatabaseFacts(t *testing.T) {
	t.Parallel()

	input := task021RuntimePreflightInput(t)
	storage := input.Health.AuthoritativeStorage
	submission := input.Health.Submission
	reference := input.Clock.ReferenceAt
	observed := reference.Add(time.Second)

	updated := tournamentusecase.ApplyRuntimeHealth(input, tournamentusecase.RuntimeHealth{
		TaskDelivery: tournamentusecase.ComponentHealth{Healthy: true, Revision: "task_delivery:healthy:ready"},
		Realtime:     tournamentusecase.ComponentHealth{Healthy: false, Revision: "realtime:degraded:stale"},
		Clock: tournamentusecase.ClockHealth{
			ObservedAt: observed, ReferenceAt: reference.Add(250 * time.Millisecond), MaxSkew: 2 * time.Second,
		},
		ClockSampled: true,
		Dependencies: []tournamentusecase.DependencyHealth{
			{Name: tournamentusecase.DependencyRedis, Healthy: false, Revision: "redis:failed"},
			{Name: tournamentusecase.DependencyObjectStorage, Healthy: false, Revision: "object_storage:failed"},
		},
	})

	require.Equal(t, storage, updated.Health.AuthoritativeStorage)
	require.Equal(t, submission, updated.Health.Submission)
	require.Equal(t, observed, updated.Clock.ObservedAt)
	require.Equal(t, reference.Add(250*time.Millisecond), updated.Clock.ReferenceAt)
	require.Equal(t, []tournamentusecase.DependencyHealth{
		{Name: tournamentusecase.DependencyObjectStorage, Healthy: false, Revision: "object_storage:failed"},
		input.Dependencies[0],
		{Name: tournamentusecase.DependencyRedis, Healthy: false, Revision: "redis:failed"},
	}, updated.Dependencies)
}

func task021RuntimePreflightInput(t *testing.T) tournamentusecase.RuntimeInput {
	t.Helper()
	normal := capacity.ProveNormal(preflightNormalCapacityInput(16))
	golden := capacity.ProveGolden(preflightGoldenCapacityInput(16))
	tournamentID := preflightCapacityID(50)
	certifiedAt := time.Date(2026, time.August, 29, 0, 30, 0, 0, time.UTC)
	capacity, err := tournamentusecase.NewCertification(
		preflightCapacityID(51), tournamentID, 8, normal, golden, certifiedAt,
	)
	if err != nil {
		t.Fatalf("NewCertification() error = %v", err)
	}
	start := certifiedAt.Add(30 * time.Minute)
	return tournamentusecase.RuntimeInput{
		TournamentID:    tournamentID,
		Preset:          domain.TournamentPresetV1,
		RosterSize:      16,
		ContentRevision: 8,
		Configuration: tournamentusecase.ConfigurationHealth{
			Valid: true, Revision: "runtime-v3",
		},
		Health: tournamentusecase.Health{
			AuthoritativeStorage: tournamentusecase.ComponentHealth{Healthy: true, Revision: "storage-v4", InternalDetail: "private-storage-error"},
			Submission:           tournamentusecase.ComponentHealth{Healthy: true, Revision: "submission-v2"},
			TaskDelivery:         tournamentusecase.ComponentHealth{Healthy: true, Revision: "delivery-v5"},
			Realtime:             tournamentusecase.ComponentHealth{Healthy: true, Revision: "realtime-v6", InternalDetail: "private-realtime-error"},
		},
		Capacity: &capacity,
		Clock: tournamentusecase.ClockHealth{
			ObservedAt: certifiedAt.Add(time.Second), ReferenceAt: certifiedAt, MaxSkew: 2 * time.Second,
		},
		Dependencies: []tournamentusecase.DependencyHealth{
			{Name: tournamentusecase.DependencyPostgres, Healthy: true, Revision: "postgres-v18"},
			{Name: tournamentusecase.DependencyRedis, Healthy: true, Revision: "redis-v8"},
			{Name: tournamentusecase.DependencyObjectStorage, Healthy: true, Revision: "seaweedfs-v3"},
		},
		Schedule: tournamentusecase.ScheduleHealth{
			StartsAt: start, MustFinishBy: start.Add(time.Hour), ProjectedDuration: 30 * time.Minute,
		},
	}
}

func cloneRuntimePreflightInput(input tournamentusecase.RuntimeInput) tournamentusecase.RuntimeInput {
	clone := input
	clone.Dependencies = append([]tournamentusecase.DependencyHealth(nil), input.Dependencies...)
	if input.Capacity != nil {
		capacity := *input.Capacity
		clone.Capacity = &capacity
	}
	return clone
}

func task021PreflightCodes(report tournamentusecase.Report) []tournamentusecase.Code {
	codes := make([]tournamentusecase.Code, len(report.Checks))
	for i, check := range report.Checks {
		codes[i] = check.Code
	}
	return codes
}

func task021FailedPreflightCodes(report tournamentusecase.Report) []tournamentusecase.Code {
	codes := make([]tournamentusecase.Code, 0)
	for _, check := range report.Checks {
		if !check.Passed {
			codes = append(codes, check.Code)
		}
	}
	return codes
}

func task021FlattenPreflight(report tournamentusecase.Report) string {
	var output strings.Builder
	for _, check := range report.Checks {
		output.WriteString(string(check.Code))
		output.WriteString(check.Explanation)
		output.WriteString(strings.Join(check.Evidence, ","))
	}
	return output.String()
}
