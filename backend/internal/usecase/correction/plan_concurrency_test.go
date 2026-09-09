package correction_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
)

func TestCorrectionPlanConcurrentReuse(t *testing.T) {
	t.Run("supports concurrent reuse without shared mutation", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		baseline, err := correctionusecase.BuildPlan(command, authority)
		require.NoError(t, err)

		const workers = 12
		results := make(chan []byte, workers)
		errors := make(chan error, workers)
		var group sync.WaitGroup
		for range workers {
			group.Add(1)
			go func() {
				defer group.Done()
				if validateErr := baseline.Validate(); validateErr != nil {
					errors <- validateErr
					return
				}
				plan, planErr := correctionusecase.BuildPlan(command, authority)
				if planErr != nil {
					errors <- planErr
					return
				}
				if validateErr := plan.Validate(); validateErr != nil {
					errors <- validateErr
					return
				}
				results <- plan.Bytes()
			}()
		}
		group.Wait()
		close(results)
		close(errors)
		for planErr := range errors {
			require.NoError(t, planErr)
		}
		for payload := range results {
			require.Equal(t, baseline.Bytes(), payload)
		}
	})
}
