package participant

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestValidatePostSeriesResolution(t *testing.T) {
	t.Parallel()

	resolved := postSeriesResolutionFixture()
	if err := validatePostSeriesResolution(resolved); err != nil {
		t.Fatalf("valid resolution rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ResolvedPostSeries)
	}{
		{name: "missing participant", mutate: func(value *ResolvedPostSeries) {
			value.Authority.ParticipantID = uuid.Nil
		}},
		{name: "non terminal Series", mutate: func(value *ResolvedPostSeries) {
			value.SeriesState = domain.SeriesStateActive
		}},
		{name: "missing result head", mutate: func(value *ResolvedPostSeries) {
			value.CurrentResultRevisionID = domain.OfficialResultRevisionID{}
		}},
		{name: "unknown action", mutate: func(value *ResolvedPostSeries) {
			value.Action = "unknown"
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := resolved
			test.mutate(&candidate)
			if !errors.Is(validatePostSeriesResolution(candidate), domain.ErrValidation) {
				t.Fatal("invalid resolution was not rejected")
			}
		})
	}
}

func TestReconcilePostSeriesFailsClosedOnCommandReuse(t *testing.T) {
	t.Parallel()

	resolved := postSeriesResolutionFixture()
	record := postSeriesRecordFixture(resolved)

	result, changed, err := reconcilePostSeries(record, resolved)
	if err != nil || changed {
		t.Fatalf("matching replay failed: changed=%t err=%v", changed, err)
	}
	if result.AcceptedAction != resolved.Action || result.ParticipantID != resolved.Authority.ParticipantID {
		t.Fatal("matching replay returned another command result")
	}

	reused := resolved
	reused.Action = usecase.PostSeriesActionLeaveLobby
	_, _, err = reconcilePostSeries(record, reused)
	if !errors.Is(err, ErrPostSeriesCommandReuse) {
		t.Fatalf("command reuse error = %v", err)
	}
}

func postSeriesResolutionFixture() ResolvedPostSeries {
	return ResolvedPostSeries{
		Authority: ParticipantCommandAuthority{
			TournamentID:         uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			RosterID:             uuid.MustParse("00000000-0000-0000-0000-000000000002"),
			PlayerID:             uuid.MustParse("00000000-0000-0000-0000-000000000003"),
			ParticipantID:        uuid.MustParse("00000000-0000-0000-0000-000000000004"),
			TournamentState:      domain.TournamentStateCompleted,
			ProjectionRevisionID: uuid.MustParse("00000000-0000-0000-0000-000000000005"),
			ProjectionRevision:   7,
		},
		SeriesID:    uuid.MustParse("00000000-0000-0000-0000-000000000006"),
		SeriesState: domain.SeriesStateCompleted,
		CurrentResultRevisionID: domain.OfficialResultRevisionID(
			uuid.MustParse("00000000-0000-0000-0000-000000000007"),
		),
		CommandID: uuid.MustParse("00000000-0000-0000-0000-000000000008"),
		Action:    usecase.PostSeriesActionAcknowledgeResult,
	}
}

func postSeriesRecordFixture(resolved ResolvedPostSeries) PostSeriesRecord {
	return PostSeriesRecord{
		CommandID:                     resolved.CommandID,
		TournamentID:                  resolved.Authority.TournamentID,
		RosterID:                      resolved.Authority.RosterID,
		SeriesID:                      resolved.SeriesID,
		ParticipantID:                 resolved.Authority.ParticipantID,
		CurrentResultRevisionID:       resolved.CurrentResultRevisionID,
		SourceProjectionRevisionID:    resolved.Authority.ProjectionRevisionID,
		SourceProjectionRevision:      resolved.Authority.ProjectionRevision,
		ResultingProjectionRevisionID: resolved.Authority.ProjectionRevisionID,
		ResultingProjectionRevision:   resolved.Authority.ProjectionRevision,
		Action:                        resolved.Action,
		OccurredAt:                    time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC),
	}
}
