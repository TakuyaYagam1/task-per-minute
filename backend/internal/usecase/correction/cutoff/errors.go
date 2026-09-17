package cutoff

import (
	"errors"
	"fmt"
)

var (
	ErrInvalid = errors.New("invalid correction")
	ErrCutoff  = errors.New("correction cutoff reached")
)

type RejectionCode string

const (
	RejectionMalformed       RejectionCode = "malformed"
	RejectionStale           RejectionCode = "stale"
	RejectionIncomplete      RejectionCode = "incomplete"
	RejectionIdentityAlias   RejectionCode = "identity_aliased"
	RejectionTerminal        RejectionCode = "terminal_incompatible"
	RejectionCrossTournament RejectionCode = "cross_tournament"
	RejectionCutoff          RejectionCode = "cutoff"
)

type Error struct {
	code   RejectionCode
	cause  error
	detail string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%v: %s: %s", e.cause, e.code, e.detail)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Code() RejectionCode {
	if e == nil {
		return ""
	}
	return e.code
}

func Code(err error) RejectionCode {
	var rejection *Error
	if errors.As(err, &rejection) {
		return rejection.Code()
	}
	return ""
}

func Reject(code RejectionCode, cause error, detail string) error {
	return &Error{code: code, cause: cause, detail: detail}
}

func rejectCorrection(code RejectionCode, cause error, detail string) error {
	return Reject(code, cause, detail)
}
