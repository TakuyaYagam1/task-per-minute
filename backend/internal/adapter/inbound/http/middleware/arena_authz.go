package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type ArenaRole string

const (
	ArenaRoleOperator    ArenaRole = "operator"
	ArenaRoleParticipant ArenaRole = "participant"
)

type ArenaAccess struct {
	Role         ArenaRole
	ActorID      string
	SessionID    string
	TournamentID uuid.UUID
}

type ArenaScopeAuthorizer interface {
	AuthorizeArena(ctx context.Context, access ArenaAccess) bool
}

type ArenaScopeAuthorizerFunc func(context.Context, ArenaAccess) bool

func (f ArenaScopeAuthorizerFunc) AuthorizeArena(ctx context.Context, access ArenaAccess) bool {
	return f != nil && f(ctx, access)
}

func ArenaAuthorization(authorizer ArenaScopeAuthorizer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, tournamentID, protected, valid := arenaRouteScope(r)
			if !protected {
				next.ServeHTTP(w, r)
				return
			}
			if !valid {
				writeProblem(w, r, http.StatusBadRequest, http.StatusText(http.StatusBadRequest), "invalid arena scope")
				return
			}

			access, authenticated, confused := arenaActorAccess(r, role, tournamentID)
			if confused {
				writeProblem(w, r, http.StatusForbidden, http.StatusText(http.StatusForbidden), "arena access denied")
				return
			}
			if !authenticated {
				writeUnauthorized(w, r, "missing arena session")
				return
			}
			if authorizer == nil || !authorizer.AuthorizeArena(r.Context(), access) {
				writeProblem(w, r, http.StatusForbidden, http.StatusText(http.StatusForbidden), "arena access denied")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

//nolint:gocyclo // The small route grammar keeps public, operator, and participant scope fail-closed in one place.
func arenaRouteScope(r *http.Request) (ArenaRole, uuid.UUID, bool, bool) {
	if r == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/arena/") {
		return "", uuid.Nil, false, true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "arena" {
		return "", uuid.Nil, false, true
	}
	if parts[3] == "public" {
		return "", uuid.Nil, false, true
	}
	if parts[3] == "operator" {
		if len(parts) >= 6 && parts[4] == "tournaments" {
			tournamentID, err := uuid.Parse(parts[5])
			return ArenaRoleOperator, tournamentID, true, err == nil && tournamentID != uuid.Nil
		}
		if len(parts) >= 5 && parts[4] == "audit" {
			tournamentID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("tournament_id")))
			return ArenaRoleOperator, tournamentID, true, err == nil && tournamentID != uuid.Nil
		}
		return ArenaRoleOperator, uuid.Nil, true, true
	}
	if len(parts) >= 6 && parts[3] == "tournaments" && parts[5] == "participant" {
		tournamentID, err := uuid.Parse(parts[4])
		return ArenaRoleParticipant, tournamentID, true, err == nil && tournamentID != uuid.Nil
	}
	return "", uuid.Nil, false, true
}

func arenaActorAccess(r *http.Request, role ArenaRole, tournamentID uuid.UUID) (ArenaAccess, bool, bool) {
	switch role {
	case ArenaRoleOperator:
		claims, operator := GetAdminClaimsFromCtx(r.Context())
		_, participant := GetPlayerFromCtx(r.Context())
		if !operator || claims.Subject == "" || claims.JTI == "" {
			return ArenaAccess{}, false, participant
		}
		return ArenaAccess{
			Role: ArenaRoleOperator, ActorID: claims.Subject, SessionID: claims.JTI, TournamentID: tournamentID,
		}, true, false
	case ArenaRoleParticipant:
		player, participant := GetPlayerFromCtx(r.Context())
		_, operator := GetAdminClaimsFromCtx(r.Context())
		if !participant || player.ID == uuid.Nil {
			return ArenaAccess{}, false, operator
		}
		return ArenaAccess{
			Role: ArenaRoleParticipant, ActorID: player.ID.String(), TournamentID: tournamentID,
		}, true, false
	default:
		return ArenaAccess{}, false, false
	}
}
