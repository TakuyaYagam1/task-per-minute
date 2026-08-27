package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestArenaGolden(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	groupID := uuid.New()
	groupRevisionID := domain.ArenaDerivedRevisionID(uuid.New())
	sourceRevisionID := domain.ArenaDerivedRevisionID(uuid.New())
	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	startedAt := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(3 * time.Minute)
	retainedAt := finishedAt.Add(time.Minute)
	firstAttemptID := uuid.New()
	secondAttemptID := uuid.New()
	firstAttempt := domain.ArenaGoldenAttempt{
		ID:              firstAttemptID,
		GroupID:         groupID,
		GroupRevisionID: groupRevisionID,
		AttemptNo:       1,
		State:           domain.ArenaGoldenAttemptStateCompleted,
		ParticipantIDs:  append([]uuid.UUID(nil), participants...),
		StartedAt:       &startedAt,
		FinishedAt:      &finishedAt,
	}
	secondAttempt := domain.ArenaGoldenAttempt{
		ID:                secondAttemptID,
		GroupID:           groupID,
		GroupRevisionID:   groupRevisionID,
		AttemptNo:         2,
		PreviousAttemptID: &firstAttemptID,
		State:             domain.ArenaGoldenAttemptStateWaitingReady,
		ParticipantIDs:    append([]uuid.UUID(nil), participants[1:]...),
	}
	group, err := domain.NewArenaGoldenGroup(domain.ArenaGoldenGroupState{
		ID:                         groupID,
		TournamentID:               tournamentID,
		RevisionID:                 groupRevisionID,
		SourceProjectionRevisionID: sourceRevisionID,
		PositionFrom:               2,
		PositionTo:                 4,
		Members: []domain.ArenaGoldenMember{
			{ParticipantID: participants[0]},
			{ParticipantID: participants[1]},
			{ParticipantID: participants[2]},
		},
	})
	if err != nil {
		t.Fatalf("valid Golden group rejected: %v", err)
	}

	changed, err := group.EstablishParticipation()
	if err != nil || !changed {
		t.Fatalf("establish participation = %v, %v", changed, err)
	}
	changed, err = group.EstablishParticipation()
	if err != nil || changed {
		t.Fatalf("repeated participation establishment = %v, %v", changed, err)
	}
	stateWithAttempts := group.Snapshot()
	stateWithAttempts.Attempts = []domain.ArenaGoldenAttempt{firstAttempt, secondAttempt}
	group, err = domain.NewArenaGoldenGroup(stateWithAttempts)
	if err != nil {
		t.Fatalf("restore Golden attempt lineage: %v", err)
	}
	changed, err = group.ExcludeParticipant(participants[2])
	if err != nil || !changed {
		t.Fatalf("exclude participant = %v, %v", changed, err)
	}
	changed, err = group.ExcludeParticipant(participants[2])
	if err != nil || changed {
		t.Fatalf("repeated exclusion = %v, %v", changed, err)
	}
	changed, err = group.RetainAttemptBeforeStart(secondAttemptID, retainedAt)
	if err != nil || !changed {
		t.Fatalf("retain pre-start attempt = %v, %v", changed, err)
	}
	changed, err = group.RetainAttemptBeforeStart(secondAttemptID, retainedAt.Add(time.Second))
	if err != nil || changed {
		t.Fatalf("repeated pre-start retention = %v, %v", changed, err)
	}

	snapshot := group.Snapshot()
	if !snapshot.ParticipationEstablished {
		t.Fatal("participation establishment was not retained")
	}
	if !snapshot.Members[2].Excluded {
		t.Fatal("participant exclusion was not retained")
	}
	if len(snapshot.Attempts) != 2 || snapshot.Attempts[1].RetainedAt == nil {
		t.Fatalf("retained attempt lineage = %+v", snapshot.Attempts)
	}
	if snapshot.Attempts[1].PreviousAttemptID == nil || *snapshot.Attempts[1].PreviousAttemptID != firstAttemptID {
		t.Fatal("attempt predecessor identity was not retained")
	}

	snapshot.ParticipationEstablished = false
	snapshot.Members[2].Excluded = false
	snapshot.Attempts[1].ParticipantIDs[0] = uuid.New()
	reloaded := group.Snapshot()
	if !reloaded.ParticipationEstablished || !reloaded.Members[2].Excluded {
		t.Fatal("caller mutated irreversible Golden state")
	}
	if reloaded.Attempts[1].ParticipantIDs[0] != participants[1] {
		t.Fatal("caller mutated retained attempt membership")
	}

	active := group.ActiveParticipantIDs()
	if len(active) != 2 || active[0] != participants[0] || active[1] != participants[1] {
		t.Fatalf("active participants = %v", active)
	}
	active[0] = uuid.New()
	if group.ActiveParticipantIDs()[0] != participants[0] {
		t.Fatal("caller mutated active participant identities")
	}
}

func TestArenaGoldenRejectsInvalidGroupAndAttemptLineage(t *testing.T) {
	t.Parallel()

	groupID := uuid.New()
	groupRevisionID := domain.ArenaDerivedRevisionID(uuid.New())
	participantIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	valid := domain.ArenaGoldenGroupState{
		ID:                         groupID,
		TournamentID:               uuid.New(),
		RevisionID:                 groupRevisionID,
		SourceProjectionRevisionID: domain.ArenaDerivedRevisionID(uuid.New()),
		PositionFrom:               1,
		PositionTo:                 3,
		Members: []domain.ArenaGoldenMember{
			{ParticipantID: participantIDs[0]},
			{ParticipantID: participantIDs[1]},
			{ParticipantID: participantIDs[2]},
		},
	}

	tests := []struct {
		name   string
		mutate func(*domain.ArenaGoldenGroupState)
	}{
		{name: "single member", mutate: func(state *domain.ArenaGoldenGroupState) { state.Members = state.Members[:1]; state.PositionTo = 1 }},
		{name: "duplicate member", mutate: func(state *domain.ArenaGoldenGroupState) {
			state.Members[2].ParticipantID = state.Members[1].ParticipantID
		}},
		{name: "position count mismatch", mutate: func(state *domain.ArenaGoldenGroupState) { state.PositionTo = 2 }},
		{name: "missing source projection", mutate: func(state *domain.ArenaGoldenGroupState) {
			state.SourceProjectionRevisionID = domain.ArenaDerivedRevisionID{}
		}},
		{name: "missing member identity", mutate: func(state *domain.ArenaGoldenGroupState) { state.Members[0].ParticipantID = uuid.Nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := cloneGoldenGroupState(valid)
			test.mutate(&state)
			if _, err := domain.NewArenaGoldenGroup(state); !errors.Is(err, domain.ErrInvalidArenaGoldenGroup) {
				t.Fatalf("error = %v, want ErrInvalidArenaGoldenGroup", err)
			}
		})
	}

	startedAt := time.Date(2026, time.August, 27, 10, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(time.Minute)
	firstID := uuid.New()
	first := domain.ArenaGoldenAttempt{
		ID:              firstID,
		GroupID:         groupID,
		GroupRevisionID: groupRevisionID,
		AttemptNo:       1,
		State:           domain.ArenaGoldenAttemptStateCompleted,
		ParticipantIDs:  append([]uuid.UUID(nil), participantIDs...),
		StartedAt:       &startedAt,
		FinishedAt:      &finishedAt,
	}
	withoutParticipation := cloneGoldenGroupState(valid)
	withoutParticipation.Attempts = []domain.ArenaGoldenAttempt{first}
	if _, err := domain.NewArenaGoldenGroup(withoutParticipation); !errors.Is(err, domain.ErrInvalidArenaGoldenGroup) {
		t.Fatalf("execution without participation error = %v", err)
	}

	wrongPreviousID := uuid.New()
	second := domain.ArenaGoldenAttempt{
		ID:                uuid.New(),
		GroupID:           groupID,
		GroupRevisionID:   groupRevisionID,
		AttemptNo:         2,
		PreviousAttemptID: &wrongPreviousID,
		State:             domain.ArenaGoldenAttemptStateWaitingReady,
		ParticipantIDs:    append([]uuid.UUID(nil), participantIDs[1:]...),
	}
	broken := cloneGoldenGroupState(valid)
	broken.ParticipationEstablished = true
	broken.Attempts = []domain.ArenaGoldenAttempt{first, second}
	if _, err := domain.NewArenaGoldenGroup(broken); !errors.Is(err, domain.ErrInvalidArenaGoldenAttempt) {
		t.Fatalf("broken attempt lineage error = %v", err)
	}

	second.PreviousAttemptID = &firstID
	second.ParticipantIDs = append(second.ParticipantIDs, uuid.New())
	broken.Attempts = []domain.ArenaGoldenAttempt{first, second}
	if _, err := domain.NewArenaGoldenGroup(broken); !errors.Is(err, domain.ErrInvalidArenaGoldenAttempt) {
		t.Fatalf("foreign attempt member error = %v", err)
	}
}

func TestArenaProjection(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	standingsArtifact := domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindStandings, EntityID: tournamentID}
	goldenArtifact := domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindGoldenGroup, EntityID: uuid.New()}
	topFourArtifact := domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindTopFour, EntityID: tournamentID}
	createdAt := time.Date(2026, time.August, 27, 11, 0, 0, 0, time.UTC)

	standingsV1 := newArenaProjection(t, tournamentID, standingsArtifact, 1, nil, createdAt, []byte(`{"round":3}`))
	standingsV1ID := standingsV1.Revision().ID()
	standingsV2 := newArenaProjection(t, tournamentID, standingsArtifact, 2, &standingsV1ID, createdAt.Add(time.Second), []byte(`{"round":4}`))
	golden := newArenaProjection(t, tournamentID, goldenArtifact, 1, nil, createdAt.Add(2*time.Second), []byte(`{"members":3}`))
	goldenID := golden.Revision().ID()
	topFour := newArenaProjection(t, tournamentID, topFourArtifact, 1, nil, createdAt.Add(3*time.Second), []byte(`{"qualified":4}`))
	topFourID := topFour.Revision().ID()
	standingsV2ID := standingsV2.Revision().ID()

	graph, err := domain.NewArenaRevisionGraph(
		[]domain.ArenaProjectionRevision{topFour, standingsV2, golden, standingsV1},
		[]domain.ArenaRevisionDependency{
			{SourceRevisionID: standingsV1ID, DerivedRevisionID: standingsV2ID},
			{SourceRevisionID: standingsV2ID, DerivedRevisionID: goldenID},
			{SourceRevisionID: goldenID, DerivedRevisionID: topFourID},
		},
	)
	if err != nil {
		t.Fatalf("valid revision graph rejected: %v", err)
	}

	current, ok := graph.CurrentRevision(standingsArtifact)
	if !ok || current.Revision().ID() != standingsV2ID {
		t.Fatalf("current standings revision = %v, %v", current.Revision().ID(), ok)
	}
	if !graph.DependsOn(topFourID, standingsV1ID) {
		t.Fatal("transitive artifact lineage was not preserved")
	}
	if graph.DependsOn(standingsV1ID, topFourID) {
		t.Fatal("dependency direction was reversed")
	}

	projections := graph.Projections()
	if len(projections) != 4 {
		t.Fatalf("projection count = %d", len(projections))
	}
	payload := projections[0].Payload()
	payload[0] = 'x'
	current, _ = graph.CurrentRevision(standingsArtifact)
	if string(current.Payload()) != `{"round":4}` {
		t.Fatal("caller mutated immutable projection payload")
	}
	dependencies := graph.Dependencies()
	dependencies[0].SourceRevisionID = domain.ArenaDerivedRevisionID(uuid.New())
	if !graph.DependsOn(topFourID, standingsV1ID) {
		t.Fatal("caller mutated immutable dependency edges")
	}
}

func TestArenaProjectionRejectsBrokenLineage(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	artifact := domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindStandings, EntityID: tournamentID}
	createdAt := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	first := newArenaProjection(t, tournamentID, artifact, 1, nil, createdAt, []byte("one"))
	firstID := first.Revision().ID()
	second := newArenaProjection(t, tournamentID, artifact, 2, &firstID, createdAt.Add(time.Second), []byte("two"))
	secondID := second.Revision().ID()

	if _, err := domain.NewArenaRevisionGraph([]domain.ArenaProjectionRevision{first, second}, nil); !errors.Is(err, domain.ErrInvalidArenaRevisionGraph) {
		t.Fatalf("missing predecessor edge error = %v", err)
	}

	cycleEdges := []domain.ArenaRevisionDependency{
		{SourceRevisionID: firstID, DerivedRevisionID: secondID},
		{SourceRevisionID: secondID, DerivedRevisionID: firstID},
	}
	if _, err := domain.NewArenaRevisionGraph([]domain.ArenaProjectionRevision{first, second}, cycleEdges); !errors.Is(err, domain.ErrInvalidArenaRevisionGraph) {
		t.Fatalf("cycle error = %v", err)
	}

	foreignID := domain.ArenaDerivedRevisionID(uuid.New())
	if _, err := domain.NewArenaRevisionGraph(
		[]domain.ArenaProjectionRevision{first},
		[]domain.ArenaRevisionDependency{{SourceRevisionID: firstID, DerivedRevisionID: foreignID}},
	); !errors.Is(err, domain.ErrInvalidArenaRevisionGraph) {
		t.Fatalf("foreign dependency error = %v", err)
	}
}

func newArenaProjection(
	t *testing.T,
	tournamentID uuid.UUID,
	artifact domain.ArenaArtifactRef,
	revisionNo int,
	previousRevisionID *domain.ArenaDerivedRevisionID,
	createdAt time.Time,
	payload []byte,
) domain.ArenaProjectionRevision {
	t.Helper()
	projection, err := domain.NewArenaProjectionRevision(
		domain.ArenaDerivedRevisionID(uuid.New()),
		tournamentID,
		artifact,
		revisionNo,
		previousRevisionID,
		createdAt,
		payload,
	)
	if err != nil {
		t.Fatalf("new projection: %v", err)
	}
	return projection
}

func cloneGoldenGroupState(state domain.ArenaGoldenGroupState) domain.ArenaGoldenGroupState {
	clone := state
	clone.Members = append([]domain.ArenaGoldenMember(nil), state.Members...)
	clone.Attempts = append([]domain.ArenaGoldenAttempt(nil), state.Attempts...)
	for i := range clone.Attempts {
		clone.Attempts[i].ParticipantIDs = append([]uuid.UUID(nil), clone.Attempts[i].ParticipantIDs...)
	}
	return clone
}
