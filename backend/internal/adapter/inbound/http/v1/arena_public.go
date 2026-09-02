package v1

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ArenaPublicProjectionService interface {
	GetPublicTournament(ctx context.Context, command ArenaTournamentReadCommand) (api.ArenaPublicTournamentResponse, error)
	GetLiveDraft(ctx context.Context, command ArenaTournamentReadCommand) (api.ArenaPublicLiveDraftResponse, error)
	GetPublicSnapshot(ctx context.Context, tournamentID api.ArenaTournamentId, cursor *api.ArenaPublicCursor) (api.ArenaPublicRecoverySnapshot, error)
}

func (c *arenaAdminController) GetArenaPublicTournament(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
) {
	service, ok := c.service.(ArenaPublicProjectionService)
	if !ok || service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if tournamentID == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := service.GetPublicTournament(r.Context(), ArenaTournamentReadCommand{TournamentID: tournamentID})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	if result.TournamentId != tournamentID || result.ProjectionRevision < 1 {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	response.WriteJSON(w, http.StatusOK, api.ArenaPublicTournamentResponse{
		TournamentId:       result.TournamentId,
		Preset:             result.Preset,
		State:              result.State,
		RosterSize:         result.RosterSize,
		StartedAt:          result.StartedAt,
		FinishedAt:         result.FinishedAt,
		ProjectionRevision: result.ProjectionRevision,
	})
}

func (c *arenaAdminController) GetArenaPublicLiveDraft(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
) {
	service, ok := c.service.(ArenaPublicProjectionService)
	if !ok || service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if tournamentID == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := service.GetLiveDraft(r.Context(), ArenaTournamentReadCommand{TournamentID: tournamentID})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	if result.TournamentId != tournamentID || result.SeriesId == uuid.Nil || result.ProjectionRevision < 1 {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	response.WriteJSON(w, http.StatusOK, api.ArenaPublicLiveDraftResponse{
		TournamentId:       result.TournamentId,
		SeriesId:           result.SeriesId,
		ProjectionRevision: result.ProjectionRevision,
		Format:             result.Format,
		State:              result.State,
		Pool:               append([]api.ArenaCategory(nil), result.Pool...),
		Actions:            append([]api.ArenaPublicDraftAction(nil), result.Actions...),
		SelectedCategories: append([]api.ArenaCategory(nil), result.SelectedCategories...),
	})
}

//nolint:gocyclo // The handler validates and copies every allowlisted public projection field explicitly.
func (c *arenaAdminController) GetArenaPublicSnapshot(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	params api.GetArenaPublicSnapshotParams,
) {
	service, ok := c.service.(ArenaPublicProjectionService)
	if !ok || service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if tournamentID == uuid.Nil || params.Cursor != nil &&
		(params.Cursor.ProjectionRevision < 1 || params.Cursor.EventSequence < 0) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := service.GetPublicSnapshot(r.Context(), tournamentID, params.Cursor)
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	if result.Tournament.TournamentId != tournamentID || result.Tournament.ProjectionRevision < 1 ||
		result.Scoreboard.TournamentId != tournamentID || result.Scoreboard.ProjectionRevision < 1 ||
		result.Bracket.TournamentId != tournamentID || result.Bracket.ProjectionRevision < 1 ||
		result.NextCursor.ProjectionRevision < 1 || result.NextCursor.EventSequence < 0 ||
		result.LiveDraft != nil && (result.LiveDraft.TournamentId != tournamentID ||
			result.LiveDraft.SeriesId == uuid.Nil || result.LiveDraft.ProjectionRevision < 1) {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var liveDraft *api.ArenaPublicLiveDraftResponse
	if result.LiveDraft != nil {
		liveDraft = &api.ArenaPublicLiveDraftResponse{
			TournamentId:       result.LiveDraft.TournamentId,
			SeriesId:           result.LiveDraft.SeriesId,
			ProjectionRevision: result.LiveDraft.ProjectionRevision,
			Format:             result.LiveDraft.Format,
			State:              result.LiveDraft.State,
			Pool:               append([]api.ArenaCategory(nil), result.LiveDraft.Pool...),
			Actions:            append([]api.ArenaPublicDraftAction(nil), result.LiveDraft.Actions...),
			SelectedCategories: append([]api.ArenaCategory(nil), result.LiveDraft.SelectedCategories...),
		}
	}

	response.WriteJSON(w, http.StatusOK, api.ArenaPublicRecoverySnapshot{
		Tournament: api.ArenaPublicTournamentResponse{
			TournamentId:       result.Tournament.TournamentId,
			Preset:             result.Tournament.Preset,
			State:              result.Tournament.State,
			RosterSize:         result.Tournament.RosterSize,
			StartedAt:          result.Tournament.StartedAt,
			FinishedAt:         result.Tournament.FinishedAt,
			ProjectionRevision: result.Tournament.ProjectionRevision,
		},
		Scoreboard: api.ArenaPublicScoreboardResponse{
			TournamentId:       result.Scoreboard.TournamentId,
			ProjectionRevision: result.Scoreboard.ProjectionRevision,
			Entries:            append([]api.ArenaPublicScoreboardEntry(nil), result.Scoreboard.Entries...),
		},
		Bracket: api.ArenaPublicBracketResponse{
			TournamentId:       result.Bracket.TournamentId,
			ProjectionRevision: result.Bracket.ProjectionRevision,
			Matches:            append([]api.ArenaPublicBracketMatch(nil), result.Bracket.Matches...),
		},
		LiveDraft: liveDraft,
		NextCursor: api.ArenaPublicRecoveryCursor{
			ProjectionRevision: result.NextCursor.ProjectionRevision,
			EventSequence:      result.NextCursor.EventSequence,
		},
	})
}
