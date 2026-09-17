package cutoff

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluateCutoffRejectsEmptyDAG(t *testing.T) {
	_, err := EvaluateCutoff(CutoffInput{})
	require.ErrorIs(t, err, ErrInvalid)
	require.Equal(t, RejectionMalformed, Code(err))
}
