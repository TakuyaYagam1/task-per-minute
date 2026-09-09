package participant

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

func TestInboundMappingsPreserveParticipantResultsAndIsolateMutableFields(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Round(0)
	actorID := uuid.New()
	action := domain.DraftActionPick
	evidence := domain.DecisionEvidence{ID: uuid.New(), NormalizedInputs: []string{"input"}, Result: []string{"result"}}
	execution := draftusecase.Execution{ID: uuid.New(), SeriesID: uuid.New(), Format: domain.SeriesFormatBO1, FirstParticipantID: actorID, SecondParticipantID: uuid.New(), Pool: []domain.Category{domain.CategoryWeb}, State: draftusecase.ExecutionStateActive, RevisionID: uuid.New(), Revision: 1, CommandID: uuid.New(), ServiceEpoch: uuid.New(), Turn: 1, CurrentActorID: &actorID, CurrentAction: &action, TurnDeadline: now, LegalCategories: []domain.Category{domain.CategoryWeb}, Actions: []draftusecase.ActionRecord{{ID: uuid.New(), DecisionEvidence: &evidence}}, SelectedCategories: []domain.Category{domain.CategoryWeb}, FirstActorDecision: evidence}

	view := draftExecutionView(execution)
	require.Equal(t, execution.ID, view.ID)
	require.Equal(t, execution.Actions[0].ID, view.Actions[0].ID)
	require.Equal(t, execution.FirstActorDecision.Result, view.FirstActorDecision.Result)
	execution.Pool[0] = domain.CategoryPwn
	execution.Actions[0].DecisionEvidence.Result[0] = "changed"
	require.Equal(t, domain.CategoryWeb, view.Pool[0])
	require.Equal(t, "result", view.Actions[0].DecisionEvidence.Result[0])

	event := readinessEventView(readiness.ReadinessEvent{CommandID: uuid.New(), Scope: readiness.ReadinessScope{WaveID: uuid.New(), WindowID: uuid.New()}, ParticipantID: actorID, Type: readiness.ReadinessEventReady, OccurredAt: now})
	require.Equal(t, readiness.ReadinessEventReady, readiness.ReadinessEventType(event.Type))
	require.NotZero(t, event.WaveID)
	require.NotZero(t, event.WindowID)
}
