package v1

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type HandlerOptions struct {
	Router           chi.Router
	AdminAuth        middleware.AdminAccessVerifier
	PlayerRepo       middleware.PlayerSessionReader
	RequestValidator api.MiddlewareFunc
	Middlewares      []api.MiddlewareFunc
}

func NewHandler(server *Server, opts HandlerOptions) http.Handler {
	middlewares := make([]api.MiddlewareFunc, 0, len(opts.Middlewares)+3)
	if opts.RequestValidator != nil {
		middlewares = append(middlewares, opts.RequestValidator)
	}
	if server != nil {
		middlewares = append(middlewares, server.publicRequestGuard())
		middlewares = append(middlewares, server.tournamentRateGuard())
	}
	if opts.AdminAuth != nil || opts.PlayerRepo != nil {
		middlewares = append(middlewares, middleware.Auth(opts.AdminAuth, opts.PlayerRepo))
	}
	middlewares = append(middlewares, opts.Middlewares...)

	return api.HandlerWithOptions(server, api.ChiServerOptions{
		BaseRouter:  opts.Router,
		Middlewares: middlewares,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			errmap.HandleError(w, r, domain.ErrValidation)
		},
	})
}
