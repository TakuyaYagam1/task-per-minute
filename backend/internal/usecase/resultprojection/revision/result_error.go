package revision

import (
	"errors"
	"fmt"
)

var ErrInvalidOfficialResultProjection = errors.New("invalid official result projection")

func invalidOfficialResultProjection(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOfficialResultProjection, fmt.Sprintf(format, arguments...))
}
