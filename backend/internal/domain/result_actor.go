package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrInvalidResultActor = errors.New("invalid result actor")

type ResultActorKind string

const (
	ResultActorServer   ResultActorKind = "server"
	ResultActorOperator ResultActorKind = "operator"
)

type ResultActor struct {
	Kind        ResultActorKind
	PrincipalID *uuid.UUID
}

func (a ResultActor) Validate() error {
	switch a.Kind {
	case ResultActorServer:
		if a.PrincipalID != nil {
			return fmt.Errorf("%w: server actor has a principal", ErrInvalidResultActor)
		}
	case ResultActorOperator:
		if a.PrincipalID == nil || *a.PrincipalID == uuid.Nil {
			return fmt.Errorf("%w: operator actor has no principal", ErrInvalidResultActor)
		}
	default:
		return fmt.Errorf("%w: unknown result actor", ErrInvalidResultActor)
	}
	return nil
}
