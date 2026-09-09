package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestResultActorValidation(t *testing.T) {
	t.Parallel()

	operatorID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tests := []struct {
		name    string
		actor   domain.ResultActor
		wantErr bool
	}{
		{name: "server", actor: domain.ResultActor{Kind: domain.ResultActorServer}},
		{name: "operator", actor: domain.ResultActor{Kind: domain.ResultActorOperator, PrincipalID: &operatorID}},
		{name: "server principal", actor: domain.ResultActor{Kind: domain.ResultActorServer, PrincipalID: &operatorID}, wantErr: true},
		{name: "operator without principal", actor: domain.ResultActor{Kind: domain.ResultActorOperator}, wantErr: true},
		{name: "unknown kind", actor: domain.ResultActor{}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.actor.Validate()
			if test.wantErr {
				require.ErrorIs(t, err, domain.ErrInvalidResultActor)
				return
			}
			require.NoError(t, err)
		})
	}
}
