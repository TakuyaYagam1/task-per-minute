package v1

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

type HandlerOptions struct {
	Router      chi.Router
	AdminAuth   *adminusecase.AuthUsecase
	PlayerRepo  usecase.PlayerRepo
	Middlewares []openapi.MiddlewareFunc
}

func NewHandler(server *Server, opts HandlerOptions) http.Handler {
	middlewares := make([]openapi.MiddlewareFunc, 0, len(opts.Middlewares)+1)
	if opts.AdminAuth != nil || opts.PlayerRepo != nil {
		middlewares = append(middlewares, middleware.Auth(opts.AdminAuth, opts.PlayerRepo))
	}
	middlewares = append(middlewares, opts.Middlewares...)

	return openapi.HandlerWithOptions(server, openapi.ChiServerOptions{
		BaseRouter:  opts.Router,
		Middlewares: middlewares,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			errmap.HandleError(w, r, apperr.ErrValidation)
		},
	})
}
