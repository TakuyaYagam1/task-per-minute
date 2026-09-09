package domain_test

import (
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestSeriesResultReasonIsLegalFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason domain.SeriesResultReason
		state  domain.SeriesState
		legal  bool
	}{
		{
			name:   "score completes series",
			reason: domain.SeriesResultReasonScoreComplete,
			state:  domain.SeriesStateCompleted,
			legal:  true,
		},
		{
			name:   "operator corrects completed series",
			reason: domain.SeriesResultReasonOperatorCorrection,
			state:  domain.SeriesStateCompleted,
			legal:  true,
		},
		{
			name:   "series cancellation cancels series",
			reason: domain.SeriesResultReasonSeriesCancelled,
			state:  domain.SeriesStateCancelled,
			legal:  true,
		},
		{
			name:   "tournament cancellation cancels series",
			reason: domain.SeriesResultReasonTournamentCancelled,
			state:  domain.SeriesStateCancelled,
			legal:  true,
		},
		{
			name:   "operator corrects cancelled series",
			reason: domain.SeriesResultReasonOperatorCorrection,
			state:  domain.SeriesStateCancelled,
			legal:  true,
		},
		{
			name:   "score cannot cancel series",
			reason: domain.SeriesResultReasonScoreComplete,
			state:  domain.SeriesStateCancelled,
		},
		{
			name:   "cancellation cannot complete series",
			reason: domain.SeriesResultReasonSeriesCancelled,
			state:  domain.SeriesStateCompleted,
		},
		{
			name:   "terminal reason cannot describe active series",
			reason: domain.SeriesResultReasonScoreComplete,
			state:  domain.SeriesStateActive,
		},
		{
			name:   "unknown reason is illegal",
			reason: domain.SeriesResultReason("unknown"),
			state:  domain.SeriesStateCompleted,
		},
		{
			name:   "unknown state is illegal",
			reason: domain.SeriesResultReasonScoreComplete,
			state:  domain.SeriesState("unknown"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.reason.IsLegalFor(test.state); got != test.legal {
				t.Fatalf("IsLegalFor(%q) = %v, want %v", test.state, got, test.legal)
			}
		})
	}
}
