package plan_test

import (
	"context"
	"sync"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan/mocks"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type goldenPlanCallResult struct {
	plan    *goldenusecase.ExactPlan
	changed bool
	err     error
}

type goldenRepositoryReturnMode string

const (
	goldenRepositoryReturnNormal    goldenRepositoryReturnMode = "normal"
	goldenRepositoryReturnNil       goldenRepositoryReturnMode = "nil"
	goldenRepositoryReturnMalformed goldenRepositoryReturnMode = "malformed"
	goldenRepositoryReturnMismatch  goldenRepositoryReturnMode = "mismatch"
)

type goldenExactPlanRepositoryState struct {
	mu                     sync.Mutex
	authorities            []goldenusecase.Authority
	loads                  int
	commits                int
	writes                 int
	conflicts              int
	stored                 *goldenusecase.ExactPlan
	reserved               map[domain.TaskVersionRef]uuid.UUID
	attemptedProofs        []string
	returnMode             goldenRepositoryReturnMode
	returnAliases          bool
	returnAuthorityAliases bool
	onConflict             func()
}

type goldenExactPlanRepositoryHarness struct {
	mock  *goldenmocks.MockPlanRepository
	state *goldenExactPlanRepositoryState
}

func newGoldenExactPlanRepository(
	t *testing.T,
	authority goldenusecase.Authority,
) *goldenExactPlanRepositoryHarness {
	t.Helper()

	state := &goldenExactPlanRepositoryState{
		authorities: []goldenusecase.Authority{authority.Snapshot()},
		reserved:    make(map[domain.TaskVersionRef]uuid.UUID),
		returnMode:  goldenRepositoryReturnNormal,
	}
	repository := goldenmocks.NewMockPlanRepository(t)
	repository.EXPECT().
		LoadAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(
			context.Context,
			goldenusecase.Scope,
		) (goldenusecase.Authority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			index := min(state.loads, len(state.authorities)-1)
			state.loads++
			if state.returnAuthorityAliases {
				return state.authorities[index], nil
			}
			return state.authorities[index].Snapshot(), nil
		}).Maybe()
	repository.EXPECT().
		Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			scope goldenusecase.Scope,
			planID uuid.UUID,
		) (*goldenusecase.ExactPlan, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.stored == nil || state.stored.Scope != scope || state.stored.PlanID != planID {
				return nil, nil
			}
			if state.returnAliases {
				return state.stored, nil
			}
			stored := state.stored.Snapshot()
			return &stored, nil
		}).Maybe()
	repository.EXPECT().
		Commit(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			plan goldenusecase.ExactPlan,
		) (*goldenusecase.ExactPlan, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.commits++
			state.attemptedProofs = append(state.attemptedProofs, plan.ProofHash)
			if plan.Validate() != nil || plan.Expected != state.authorities[len(state.authorities)-1].Expectation() {
				return nil, false, domain.ErrConflict
			}
			if state.conflicts > 0 {
				state.conflicts--
				if state.onConflict != nil {
					state.onConflict()
					state.onConflict = nil
				}
				return nil, false, domain.ErrConflict
			}
			if state.stored != nil {
				if state.stored.PlanID == plan.PlanID {
					stored := state.stored.Snapshot()
					return &stored, false, nil
				}
				return nil, false, domain.ErrConflict
			}
			for _, group := range plan.Groups {
				for _, edge := range group.Edges {
					ref := domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
					if _, exists := state.reserved[ref]; exists {
						return nil, false, domain.ErrConflict
					}
				}
			}
			stored := plan.Snapshot()
			state.stored = &stored
			state.writes++
			for _, group := range stored.Groups {
				for _, edge := range group.Edges {
					ref := domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
					state.reserved[ref] = edge.ReservationID
				}
			}
			switch state.returnMode {
			case goldenRepositoryReturnNormal:
				if state.returnAliases {
					return state.stored, true, nil
				}
				result := stored.Snapshot()
				return &result, true, nil
			case goldenRepositoryReturnNil:
				return nil, true, nil
			case goldenRepositoryReturnMalformed:
				malformed := stored.Snapshot()
				malformed.ProofHash = "bad"
				return &malformed, true, nil
			case goldenRepositoryReturnMismatch:
				mismatch := stored.Snapshot()
				mismatch.PlanID = planGoldenPlanID(9993)
				return &mismatch, false, nil
			}
			return nil, false, domain.ErrInternal
		}).Maybe()
	return &goldenExactPlanRepositoryHarness{mock: repository, state: state}
}

func (r *goldenExactPlanRepositoryState) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func (r *goldenExactPlanRepositoryState) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *goldenExactPlanRepositoryState) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func goldenExactPlanSuccessorAuthority(
	t *testing.T,
	current goldenusecase.Authority,
) goldenusecase.Authority {
	t.Helper()
	standings := planCloneSwissNormalStandings(current.Source.Standings)
	standings[len(standings)-1].Buchholz++
	previousSourceID := current.Source.RevisionID
	source, err := goldenusecase.NewStandingsProjection(
		current.Source.TournamentID,
		current.Source.ProjectionID,
		planGoldenPlanRevisionID(9800),
		current.Source.RevisionNo+1,
		&previousSourceID,
		true,
		standings,
	)
	require.NoError(t, err)
	partition, err := goldenusecase.PartitionTies(source)
	require.NoError(t, err)
	seeds := partition.Groups()
	require.Len(t, seeds, len(current.Groups))

	groups := make([]goldenusecase.GroupAuthority, len(current.Groups))
	for index, group := range current.Groups {
		previousGroupID := group.Revision.RevisionID()
		command := goldenusecase.GroupRevisionCommand{
			TournamentID:                current.Scope.TournamentID,
			GroupID:                     group.Revision.GroupID(),
			RevisionID:                  planGoldenPlanRevisionID(9810 + index),
			RevisionNo:                  group.Revision.RevisionNo() + 1,
			PreviousRevisionID:          &previousGroupID,
			ExpectedSourceRevisionID:    source.RevisionID,
			ExpectedSourcePayloadDigest: source.PayloadDigest,
			PositionFrom:                seeds[index].PositionFrom,
			PositionTo:                  seeds[index].PositionTo,
		}
		revision, buildErr := goldenusecase.BuildGroupRevision(command, source, seeds[index], &group.Revision, nil)
		require.NoError(t, buildErr)
		groups[index] = goldenusecase.GroupAuthority{
			Revision:             revision,
			ActiveParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
		}
	}

	successor := current.Snapshot()
	successor.Source = source
	successor.Groups = groups
	successor.Revisions.SourceProjectionRevisionID = source.RevisionID
	successor.Revisions.GroupSetRevisionID = planGoldenPlanID(9820)
	successor.Revisions.GroupSetRevision++
	built, err := goldenusecase.BuildAuthority(successor)
	require.NoError(t, err)
	return built
}

func goldenTaskNumber(id uuid.UUID) int {
	value := 0
	for _, character := range id.String()[24:] {
		value = value*10 + int(character-'0')
	}
	return value
}

func cloneGoldenGroupCommands(input []goldenusecase.GroupCommand) []goldenusecase.GroupCommand {
	return append([]goldenusecase.GroupCommand(nil), input...)
}

func changedGoldenPlanCommand(command goldenusecase.Command, base int) goldenusecase.Command {
	changed := command
	changed.PlanID = planGoldenPlanID(base)
	changed.PlanRevisionID = planGoldenPlanID(base + 1)
	changed.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
	for groupIndex := range changed.GroupCommands {
		for edgeIndex := range changed.GroupCommands[groupIndex].EdgeIDs {
			value := base + 10 + groupIndex*20 + edgeIndex*3
			changed.GroupCommands[groupIndex].EdgeIDs[edgeIndex] = planGoldenPlanID(value)
			changed.GroupCommands[groupIndex].ReservationIDs[edgeIndex] = planGoldenPlanID(value + 1)
			changed.GroupCommands[groupIndex].SnapshotIDs[edgeIndex] = planGoldenPlanID(value + 2)
		}
	}
	return changed
}
