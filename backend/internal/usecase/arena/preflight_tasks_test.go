package arena_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestPreflightTaskHealth(t *testing.T) {
	t.Parallel()

	wantCodes := []arena.ArenaPreflightCode{
		arena.PreflightCodeTaskPoolsValid,
		arena.PreflightCodeTaskInventory,
		arena.PreflightCodeTaskMissing,
		arena.PreflightCodeTaskDisabled,
		arena.PreflightCodeTaskUnhealthy,
		arena.PreflightCodeTaskMutable,
		arena.PreflightCodeTaskExposed,
		arena.PreflightCodeTaskWrongPool,
	}

	t.Run("accepts locked healthy private versions without exposing details", func(t *testing.T) {
		t.Parallel()

		input := validTaskHealthPreflightInput()
		report := arena.EvaluateTaskHealthPreflight(input)
		if !report.Passed() {
			t.Fatalf("valid report failed: %+v", report)
		}
		if got := preflightCodes(report); !reflect.DeepEqual(got, wantCodes) {
			t.Fatalf("codes = %v, want %v", got, wantCodes)
		}
		serialized := fmt.Sprintf("%+v", report)
		if strings.Contains(serialized, "FLAG{private-health-detail}") {
			t.Fatal("preflight report exposed internal health detail")
		}

		reordered := input
		reordered.Versions = append([]arena.TaskVersionHealth(nil), input.Versions...)
		slices.Reverse(reordered.Versions)
		if other := arena.EvaluateTaskHealthPreflight(reordered); !reflect.DeepEqual(other, report) {
			t.Fatalf("reordered report = %+v, want %+v", other, report)
		}
	})

	t.Run("uses one stable code for each rejected state", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			code   arena.ArenaPreflightCode
			mutate func(*arena.TaskHealthPreflightInput)
		}{
			{
				name: "missing", code: arena.PreflightCodeTaskMissing,
				mutate: func(in *arena.TaskHealthPreflightInput) { in.Versions = in.Versions[1:] },
			},
			{
				name: "disabled", code: arena.PreflightCodeTaskDisabled,
				mutate: func(in *arena.TaskHealthPreflightInput) { in.Versions[0].Enabled = false },
			},
			{
				name: "unhealthy", code: arena.PreflightCodeTaskUnhealthy,
				mutate: func(in *arena.TaskHealthPreflightInput) { in.Versions[0].Healthy = false },
			},
			{
				name: "mutable", code: arena.PreflightCodeTaskMutable,
				mutate: func(in *arena.TaskHealthPreflightInput) { in.Versions[0].MutationLocked = false },
			},
			{
				name: "public", code: arena.PreflightCodeTaskExposed,
				mutate: func(in *arena.TaskHealthPreflightInput) { in.Versions[0].PubliclyExposed = true },
			},
			{
				name: "wrong pool", code: arena.PreflightCodeTaskWrongPool,
				mutate: func(in *arena.TaskHealthPreflightInput) {
					in.Versions[0].PoolRevisionID = task020ID(99)
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				input := validTaskHealthPreflightInput()
				test.mutate(&input)
				report := arena.EvaluateTaskHealthPreflight(input)
				check := preflightCheck(t, report, test.code)
				if check.Passed || len(check.Evidence) == 0 || report.Passed() {
					t.Fatalf("check = %+v, report passed = %v", check, report.Passed())
				}
				if strings.Contains(fmt.Sprintf("%+v", report), "FLAG{private-health-detail}") {
					t.Fatal("failed preflight exposed internal health detail")
				}
			})
		}
	})

	t.Run("fails closed on overlapping pools and duplicate inventory", func(t *testing.T) {
		t.Parallel()

		input := validTaskHealthPreflightInput()
		input.GoldenPool.Versions[0].TaskID = input.NormalPool.Versions[0].TaskID
		input.Versions = append(input.Versions, input.Versions[0])
		report := arena.EvaluateTaskHealthPreflight(input)
		if preflightCheck(t, report, arena.PreflightCodeTaskPoolsValid).Passed {
			t.Fatal("overlapping pools passed")
		}
		if preflightCheck(t, report, arena.PreflightCodeTaskInventory).Passed {
			t.Fatal("duplicate inventory passed")
		}
	})
}

func validTaskHealthPreflightInput() arena.TaskHealthPreflightInput {
	content := task020ContentInput()
	versions := make([]arena.TaskVersionHealth, 0, len(content.NormalPool.Versions)+len(content.GoldenPool.Versions))
	versions = appendTaskVersionHealth(versions, content.NormalPool)
	versions = appendTaskVersionHealth(versions, content.GoldenPool)
	return arena.TaskHealthPreflightInput{
		NormalPool: content.NormalPool,
		GoldenPool: content.GoldenPool,
		Versions:   versions,
	}
}

func appendTaskVersionHealth(
	out []arena.TaskVersionHealth,
	pool arena.TaskPoolRevision,
) []arena.TaskVersionHealth {
	for _, version := range pool.Versions {
		out = append(out, arena.TaskVersionHealth{
			TaskID:               version.TaskID,
			Version:              version.Version,
			PoolRevisionID:       pool.ID,
			PoolKind:             pool.Kind,
			Exists:               true,
			Enabled:              true,
			Healthy:              true,
			MutationLocked:       true,
			InternalHealthDetail: "FLAG{private-health-detail}",
		})
	}
	return out
}
