package result

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestRestoreOrdinaryOfficialResultHead(t *testing.T) {
	fixture := task053SeriesResultFixture(t)
	plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
	require.NoError(t, err)
	head := plan.Revision().Head()
	head.SourceProjection = ordinarySource(t, head.SourceProjection, head.ID.UUID(), head.Ordinal, nil)
	require.Error(t, head.Validate(), "a literal head cannot authorize an origin alias")
	restored, err := RestoreOrdinaryOfficialResultHead(head)
	require.NoError(t, err)
	require.NoError(t, restored.Validate())
	require.NoError(t, restored.Clone().Validate())
	require.NoError(t, officialResultRevisionFromHead(restored).Head().Validate())
	fixture.command.ExpectedSourceProjection = head.SourceProjection
	require.Error(t, validateOfficialLocalUUIDRoles(fixture.command), "new commands cannot restore themselves")
	for name, change := range map[string]func(*OfficialResultRevisionHead){
		"current": func(h *OfficialResultRevisionHead) { h.ID = domain.OfficialResultRevisionID(uuid.New()) },
		"previous": func(h *OfficialResultRevisionHead) {
			id := domain.OfficialResultRevisionID(uuid.New())
			h.PreviousRevisionID = &id
		},
		"ordinal":     func(h *OfficialResultRevisionHead) { h.Ordinal++ },
		"scope":       func(h *OfficialResultRevisionHead) { h.Scope.SeriesID = uuid.New() },
		"command":     func(h *OfficialResultRevisionHead) { h.CommandID = h.ID.UUID() },
		"participant": func(h *OfficialResultRevisionHead) { id := h.ID.UUID(); h.Outcome.WinnerID = &id },
	} {
		t.Run(name, func(t *testing.T) {
			changed := restored.Clone()
			change(&changed)
			require.Error(t, changed.Validate())
			_, err := RestoreOrdinaryOfficialResultHead(changed)
			require.Error(t, err)
		})
	}
}

func TestRestoreOrdinarySeriesScoreHead(t *testing.T) {
	fixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
	plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
	require.NoError(t, err)
	head := plan.Revision().Head()
	previous := domain.DerivedRevisionID(head.PreviousRevisionID.UUID())
	head.SourceProjection = ordinarySource(t, head.SourceProjection, head.ID.UUID(), head.Ordinal, &previous)
	require.Error(t, head.Validate(), "a literal head cannot authorize an origin alias")
	restored, err := RestoreOrdinarySeriesScoreHead(head)
	require.NoError(t, err)
	require.NoError(t, restored.Validate())
	require.NoError(t, restored.Clone().Validate())
	require.NoError(t, seriesScoreRevisionFromHead(restored).Head().Validate())
	fixture.command.ExpectedSourceProjection = head.SourceProjection
	require.Error(t, validateScoreCommandUUIDRoles(fixture.command), "new commands cannot restore themselves")
	for name, change := range map[string]func(*SeriesScoreRevisionHead){
		"current": func(h *SeriesScoreRevisionHead) { h.ID = domain.SeriesScoreRevisionID(uuid.New()) },
		"previous": func(h *SeriesScoreRevisionHead) {
			id := domain.SeriesScoreRevisionID(uuid.New())
			h.PreviousRevisionID = &id
		},
		"current previous": func(h *SeriesScoreRevisionHead) { id := h.ID; h.PreviousRevisionID = &id },
		"ordinal":          func(h *SeriesScoreRevisionHead) { h.Ordinal++ },
		"scope":            func(h *SeriesScoreRevisionHead) { h.Scope.SeriesID = uuid.New() },
		"command":          func(h *SeriesScoreRevisionHead) { h.CommandID = h.ID.UUID() },
		"participant":      func(h *SeriesScoreRevisionHead) { h.FirstParticipantID = h.ID.UUID() },
		"game":             func(h *SeriesScoreRevisionHead) { h.Attempts[0].GameID = h.ID.UUID() },
	} {
		t.Run(name, func(t *testing.T) {
			changed := restored.Clone()
			change(&changed)
			require.Error(t, changed.Validate())
			_, err := RestoreOrdinarySeriesScoreHead(changed)
			require.Error(t, err)
		})
	}
}

func TestRestoreCorrectionOfficialResultHeadRequiresExactPersistedBinding(t *testing.T) {
	fixture := task053GameCorrectionFixture(t)
	plan, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
	require.NoError(t, err)
	head := plan.Revision().Head()
	previousSourceID := domain.DerivedRevisionID(head.PreviousRevisionID.UUID())
	nodeID := uuid.New()
	head.SourceProjection = task053Projection(
		t, nodeID, head.Scope.TournamentID, domain.ArtifactKindGameResult,
		head.Scope.GameID, head.Ordinal, &previousSourceID, "persisted correction Game result",
	)
	binding := PersistedCorrectionSourceBinding{
		TournamentID: head.Scope.TournamentID, RosterID: uuid.New(), SeriesID: head.Scope.SeriesID,
		EntityID: head.Scope.GameID, ArtifactKind: domain.ArtifactKindGameResult,
		CorrectionCommandID: head.CommandID, HeadCommandID: head.CommandID,
		SourceID: head.ID.UUID(), NodeID: nodeID,
		PreviousSourceID: head.PreviousRevisionID.UUID(), PreviousNodeID: previousSourceID.UUID(),
		NodeRevision: head.Ordinal,
	}
	require.Error(t, head.Validate(), "a literal correction alias has no persisted binding authority")
	restored, err := RestoreCorrectionOfficialResultHead(head, binding)
	require.NoError(t, err)
	require.NoError(t, restored.Validate())
	require.True(t, restored.HasCorrectionSourceIdentity())
	require.False(t, head.HasCorrectionSourceIdentity())
	require.NoError(t, restored.Clone().Validate(), "Clone must retain the private persisted binding marker")
	require.NoError(t, officialResultRevisionFromHead(restored).Head().Validate())

	for name, change := range map[string]func(*OfficialResultRevisionHead, *PersistedCorrectionSourceBinding){
		"tournament": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) { b.TournamentID = uuid.New() },
		"roster":     func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) { b.RosterID = uuid.Nil },
		"series":     func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) { b.SeriesID = uuid.New() },
		"entity":     func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) { b.EntityID = uuid.New() },
		"kind": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) {
			b.ArtifactKind = domain.ArtifactKindSeriesResult
		},
		"correction command": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) {
			b.CorrectionCommandID = uuid.Nil
		},
		"head command": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) {
			b.HeadCommandID = uuid.New()
		},
		"source": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) { b.SourceID = uuid.New() },
		"previous source": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) {
			b.PreviousSourceID = uuid.New()
		},
		"node": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) { b.NodeID = uuid.New() },
		"previous node": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) {
			b.PreviousNodeID = uuid.New()
		},
		"revision": func(_ *OfficialResultRevisionHead, b *PersistedCorrectionSourceBinding) { b.NodeRevision++ },
		"nil predecessor": func(h *OfficialResultRevisionHead, _ *PersistedCorrectionSourceBinding) {
			h.PreviousRevisionID = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := head.Clone()
			changedBinding := binding
			change(&changed, &changedBinding)
			_, restoreErr := RestoreCorrectionOfficialResultHead(changed, changedBinding)
			require.Error(t, restoreErr)
		})
	}

	markerLost := restored.Clone()
	markerLost.correctionSource = persistedCorrectionSourceBinding{}
	require.Error(t, markerLost.Validate())
}

func TestRestoreCorrectionSeriesScoreHeadRequiresExactPersistedBinding(t *testing.T) {
	fixture := task053ReplaceScoreFixture(t)
	plan, err := PlanSeriesScoreRevision(fixture.command, fixture.authority, fixture.recordedAt)
	require.NoError(t, err)
	head := plan.Revision().Head()
	previousSourceID := domain.DerivedRevisionID(head.PreviousRevisionID.UUID())
	nodeID := uuid.New()
	head.SourceProjection = task053Projection(
		t, nodeID, head.Scope.TournamentID, domain.ArtifactKindSeriesScore,
		head.Scope.SeriesID, head.Ordinal, &previousSourceID, "persisted correction Series score",
	)
	binding := PersistedCorrectionSourceBinding{
		TournamentID: head.Scope.TournamentID, RosterID: uuid.New(), SeriesID: head.Scope.SeriesID,
		EntityID: head.Scope.SeriesID, ArtifactKind: domain.ArtifactKindSeriesScore,
		CorrectionCommandID: uuid.New(), HeadCommandID: head.CommandID,
		SourceID: head.ID.UUID(), NodeID: nodeID,
		PreviousSourceID: head.PreviousRevisionID.UUID(), PreviousNodeID: previousSourceID.UUID(),
		NodeRevision: head.Ordinal,
	}
	require.Error(t, head.Validate(), "a literal correction alias has no persisted binding authority")
	restored, err := RestoreCorrectionSeriesScoreHead(head, binding)
	require.NoError(t, err)
	require.NoError(t, restored.Validate())
	require.True(t, restored.HasCorrectionSourceIdentity())
	require.False(t, head.HasCorrectionSourceIdentity())
	require.NoError(t, restored.Clone().Validate(), "Clone must retain the private persisted binding marker")
	require.NoError(t, seriesScoreRevisionFromHead(restored).Head().Validate())

	wrongCommand := binding
	wrongCommand.HeadCommandID = uuid.New()
	_, err = RestoreCorrectionSeriesScoreHead(head, wrongCommand)
	require.Error(t, err)
	wrongNode := binding
	wrongNode.NodeID = uuid.New()
	_, err = RestoreCorrectionSeriesScoreHead(head, wrongNode)
	require.Error(t, err)
	withoutPredecessor := head.Clone()
	withoutPredecessor.PreviousRevisionID = nil
	_, err = RestoreCorrectionSeriesScoreHead(withoutPredecessor, binding)
	require.Error(t, err)

	markerLost := restored.Clone()
	markerLost.correctionSource = persistedCorrectionSourceBinding{}
	require.Error(t, markerLost.Validate())
}

func ordinarySource(t *testing.T, source domain.DerivedRevision, id uuid.UUID, ordinal int, previous *domain.DerivedRevisionID) domain.DerivedRevision {
	t.Helper()
	projection, err := domain.NewProjectionRevision(domain.DerivedRevisionID(id), source.TournamentID(), source.Artifact(), ordinal, previous, source.CreatedAt(), []byte("ordinary result node"))
	require.NoError(t, err)
	return projection.Revision()
}
