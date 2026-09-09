package authority

import (
	"errors"
	"fmt"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

var ErrForbidden = errors.New("execution authority is forbidden for this process")

func invalidClaim(message string) error {
	return fmt.Errorf("%w: %s", authoritydomain.ErrInvalid, message)
}
