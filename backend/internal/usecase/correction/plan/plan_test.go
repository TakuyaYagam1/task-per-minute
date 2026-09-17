package plan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRejectsEmptyAuthority(t *testing.T) {
	_, err := Validate(Command{}, Authority{})
	require.ErrorIs(t, err, ErrInvalid)
}
