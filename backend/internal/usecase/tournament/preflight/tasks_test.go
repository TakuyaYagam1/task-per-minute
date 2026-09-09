package preflight_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestPreflightTaskHealth(t *testing.T) {
	t.Parallel()

	wantCodes := []tournamentusecase.Code{
		tournamentusecase.CodeTaskPoolsValid,
		tournamentusecase.CodeTaskInventory,
		tournamentusecase.CodeTaskMissing,
		tournamentusecase.CodeTaskDisabled,
		tournamentusecase.CodeTaskUnhealthy,
		tournamentusecase.CodeTaskMutable,
		tournamentusecase.CodeTaskExposed,
		tournamentusecase.CodeTaskWrongPool,
	}

	t.Run("accepts locked healthy private versions without exposing details", func(t *testing.T) {
		t.Parallel()

		input := validTaskHealthPreflightInput()
		report := tournamentusecase.EvaluateTaskHealth(input)
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
		reordered.Versions = append([]domain.TaskVersionHealth(nil), input.Versions...)
		slices.Reverse(reordered.Versions)
		if other := tournamentusecase.EvaluateTaskHealth(reordered); !reflect.DeepEqual(other, report) {
			t.Fatalf("reordered report = %+v, want %+v", other, report)
		}
	})

	t.Run("uses one stable code for each rejected state", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			code   tournamentusecase.Code
			mutate func(*tournamentusecase.TaskHealthInput)
		}{
			{
				name: "missing", code: tournamentusecase.CodeTaskMissing,
				mutate: func(in *tournamentusecase.TaskHealthInput) { in.Versions = in.Versions[1:] },
			},
			{
				name: "disabled", code: tournamentusecase.CodeTaskDisabled,
				mutate: func(in *tournamentusecase.TaskHealthInput) { in.Versions[0].Enabled = false },
			},
			{
				name: "unhealthy", code: tournamentusecase.CodeTaskUnhealthy,
				mutate: func(in *tournamentusecase.TaskHealthInput) { in.Versions[0].Healthy = false },
			},
			{
				name: "mutable", code: tournamentusecase.CodeTaskMutable,
				mutate: func(in *tournamentusecase.TaskHealthInput) { in.Versions[0].MutationLocked = false },
			},
			{
				name: "public", code: tournamentusecase.CodeTaskExposed,
				mutate: func(in *tournamentusecase.TaskHealthInput) { in.Versions[0].PubliclyExposed = true },
			},
			{
				name: "wrong pool", code: tournamentusecase.CodeTaskWrongPool,
				mutate: func(in *tournamentusecase.TaskHealthInput) {
					in.Versions[0].PoolRevisionID = preflightContentID(99)
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				input := validTaskHealthPreflightInput()
				test.mutate(&input)
				report := tournamentusecase.EvaluateTaskHealth(input)
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
		report := tournamentusecase.EvaluateTaskHealth(input)
		if preflightCheck(t, report, tournamentusecase.CodeTaskPoolsValid).Passed {
			t.Fatal("overlapping pools passed")
		}
		if preflightCheck(t, report, tournamentusecase.CodeTaskInventory).Passed {
			t.Fatal("duplicate inventory passed")
		}
	})
}

func validTaskHealthPreflightInput() tournamentusecase.TaskHealthInput {
	content := preflightContentInput()
	versions := make([]domain.TaskVersionHealth, 0, len(content.NormalPool.Versions)+len(content.GoldenPool.Versions))
	versions = appendTaskVersionHealth(versions, content.NormalPool)
	versions = appendTaskVersionHealth(versions, content.GoldenPool)
	return tournamentusecase.TaskHealthInput{
		NormalPool: content.NormalPool,
		GoldenPool: content.GoldenPool,
		Versions:   versions,
	}
}

func appendTaskVersionHealth(
	out []domain.TaskVersionHealth,
	pool domain.TaskPoolRevision,
) []domain.TaskVersionHealth {
	for _, version := range pool.Versions {
		out = append(out, domain.TaskVersionHealth{
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
