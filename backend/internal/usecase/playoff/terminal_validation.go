package playoff

import (
	"bytes"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (a SemifinalStageAuthority) valid() error {
	if a.StageCommandID == uuid.Nil || a.RosterID == uuid.Nil || a.Bracket.Validate() != nil ||
		!domain.IsValidServerTime(a.RecordedAt) || a.Configuration.Validate() != nil || len(a.Series) != 2 {
		return terminalConflict("invalid semifinal stage authority")
	}
	return nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (p FinalDraftPlan) valid() error {
	if p.StageCommandID == uuid.Nil || p.RosterID == uuid.Nil || !p.IDs.Valid() ||
		!domain.IsValidServerTime(p.CreatedAt) || len(p.Advancement) != 2 ||
		p.Series.Validate() != nil || p.Series.ID != p.IDs.FinalSeriesID ||
		p.Series.Format != domain.SeriesFormatBO3 || p.Series.State != domain.SeriesStatePlanned ||
		p.Category.Validate() != nil || p.Category.ID != p.IDs.CategoryRevisionID ||
		p.Category.SeriesID != p.Series.ID || p.Draft.Validate() != nil ||
		p.Draft.ID != p.IDs.DraftID || p.Draft.SeriesID != p.Series.ID ||
		!sameFinalParticipants(p.Series.FirstParticipantID, p.Series.SecondParticipantID, p.Draft.FirstParticipantID, p.Draft.SecondParticipantID) ||
		p.Draft.RevisionID != p.IDs.DraftInitialRevisionID ||
		p.Draft.State != "active" {
		return terminalConflict("invalid final draft plan")
	}
	return nil
}

func (a FinalDraftAuthority) valid(command TerminalDraftCommand) error {
	if a.StageCommandID == uuid.Nil || a.RosterID == uuid.Nil || a.Bracket.Validate() != nil ||
		len(a.Advancement) != 2 || a.ExpectedSeriesRevision < 1 || !a.IDs.Valid() ||
		a.Draft.Validate() != nil || a.Draft.ID != command.DraftID ||
		a.Draft.SeriesID != command.SeriesID || a.Draft.State != "completed" ||
		!domain.IsValidServerTime(a.RecordedAt) {
		return terminalConflict("invalid completed final draft authority")
	}
	return nil
}

func (a FinalSettlementAuthority) valid() error {
	if err := a.validBase(); err != nil {
		return err
	}
	if len(a.Bindings) != 3 || !hasFinalBindings(a.Bindings, a.IDs) {
		return terminalConflict("invalid final settlement bindings")
	}
	return nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (a FinalSettlementAuthority) validBase() error {
	if a.StageCommandID == uuid.Nil || a.RosterID == uuid.Nil || a.Bracket.Validate() != nil ||
		len(a.Advancement) != 2 || !a.IDs.Valid() || a.Draft.Validate() != nil ||
		a.Draft.ID != a.IDs.DraftID || a.Draft.SeriesID != a.IDs.FinalSeriesID ||
		a.Draft.State != "completed" || !domain.IsValidServerTime(a.RecordedAt) ||
		a.Progression.Progression.Game.Validate() != nil || a.Progression.Progression.ScoreRevision.ID.IsZero() ||
		a.Progression.Progression.ScoreRevision.SeriesID != a.IDs.FinalSeriesID {
		return terminalConflict("invalid final settlement authority")
	}
	if a.Progression.Progression.ScoreRevision.RecordedAt.After(a.RecordedAt) ||
		a.Progression.Progression.Game.ResultRevisionID == nil {
		return terminalConflict("final settlement evidence is stale")
	}
	if a.Progression.Progression.Next != nil {
		if a.Progression.Progression.Next.GameID != a.IDs.SecondGameID && a.Progression.Progression.Next.GameID != a.IDs.ThirdGameID {
			return terminalConflict("unexpected final continuation identity")
		}
		if !a.Progression.ChampionRevisionID.IsZero() || !a.Progression.RecordedAt.IsZero() {
			return terminalConflict("continuing final has terminal fields")
		}
	}
	return nil
}

func (b FinalGameBinding) valid() bool {
	return b.GameID != uuid.Nil && b.AssignmentID != uuid.Nil && b.AssignmentRevision >= 1 &&
		b.PlanID != uuid.Nil && b.PlanRevisionID != uuid.Nil && b.BranchID != uuid.Nil &&
		b.ReservationID != uuid.Nil && b.SnapshotID != uuid.Nil && b.DeadlineSeconds > 0 &&
		!bytes.Equal(b.ContentDigest[:], make([]byte, len(b.ContentDigest)))
}

func hasFinalBindings(bindings []FinalGameBinding, ids FinalStageIDs) bool {
	if len(bindings) != 3 {
		return false
	}
	wanted := map[uuid.UUID]bool{
		ids.FirstGameID: false, ids.SecondGameID: false, ids.ThirdGameID: false,
	}
	for _, binding := range bindings {
		if !binding.valid() {
			return false
		}
		if seen, exists := wanted[binding.GameID]; !exists || seen {
			return false
		}
		wanted[binding.GameID] = true
	}
	for _, found := range wanted {
		if !found {
			return false
		}
	}
	return true
}
