package v1

import (
	"errors"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/request"
)

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, fallback error) bool {
	if err := request.DecodeJSON(r, dst); err != nil {
		if errors.Is(err, request.ErrUnsupportedMediaType) {
			writeProblem(w, r, http.StatusUnsupportedMediaType, request.ErrUnsupportedMediaType.Error())
			return false
		}
		if errors.Is(err, request.ErrBodyTooLarge) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, request.ErrBodyTooLarge.Error())
			return false
		}
		errmap.HandleError(w, r, fallback)
		return false
	}
	return true
}
