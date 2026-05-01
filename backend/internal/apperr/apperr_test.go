package apperr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
)

func TestSentinels_IdentityIs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *apperr.Error
		code apperr.Code
	}{
		{"player_not_found", apperr.ErrPlayerNotFound, apperr.CodePlayerNotFound},
		{"username_taken", apperr.ErrUsernameTaken, apperr.CodeUsernameTaken},
		{"username_invalid", apperr.ErrUsernameInvalid, apperr.CodeUsernameInvalid},
		{"player_in_duel", apperr.ErrPlayerInDuel, apperr.CodePlayerInDuel},
		{"invalid_session", apperr.ErrInvalidSession, apperr.CodeInvalidSession},
		{"task_not_found", apperr.ErrTaskNotFound, apperr.CodeTaskNotFound},
		{"task_in_use", apperr.ErrTaskInUse, apperr.CodeTaskInUse},
		{"task_validation", apperr.ErrTaskValidation, apperr.CodeTaskValidation},
		{"duel_not_found", apperr.ErrDuelNotFound, apperr.CodeDuelNotFound},
		{"duel_finished", apperr.ErrDuelFinished, apperr.CodeDuelFinished},
		{"duel_deadline_passed", apperr.ErrDuelDeadlinePassed, apperr.CodeDuelDeadlinePassed},
		{"flag_incorrect", apperr.ErrFlagIncorrect, apperr.CodeFlagIncorrect},
		{"not_duel_participant", apperr.ErrNotDuelParticipant, apperr.CodeNotDuelParticipant},
		{"invalid_credentials", apperr.ErrInvalidCredentials, apperr.CodeInvalidCredentials},
		{"token_expired", apperr.ErrTokenExpired, apperr.CodeTokenExpired},
		{"token_revoked", apperr.ErrTokenRevoked, apperr.CodeTokenRevoked},
		{"internal", apperr.ErrInternal, apperr.CodeInternal},
		{"validation", apperr.ErrValidation, apperr.CodeValidation},
		{"conflict", apperr.ErrConflict, apperr.CodeConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.err.Code != tt.code {
				t.Errorf("Code = %q, want %q", tt.err.Code, tt.code)
			}
			if tt.err.Error() == "" {
				t.Errorf("Error() returned empty string")
			}
			if !errors.Is(tt.err, tt.err) {
				t.Errorf("errors.Is(self, self) = false, want true")
			}
		})
	}
}

func TestWrap_PreservesIdentity(t *testing.T) {
	t.Parallel()

	cause := errors.New("pgx: no rows")
	wrapped := apperr.Wrap(cause, apperr.ErrPlayerNotFound)

	if !errors.Is(wrapped, apperr.ErrPlayerNotFound) {
		t.Errorf("errors.Is(wrapped, ErrPlayerNotFound) = false, want true")
	}
	if errors.Is(wrapped, apperr.ErrTaskNotFound) {
		t.Errorf("errors.Is(wrapped, ErrTaskNotFound) = true, want false")
	}
	if !errors.Is(wrapped, cause) {
		t.Errorf("errors.Is(wrapped, cause) = false, want true (chain unwraps to cause)")
	}
}

func TestWrap_AsExtractsAppError(t *testing.T) {
	t.Parallel()

	cause := errors.New("pgx: no rows")
	wrapped := apperr.Wrap(cause, apperr.ErrDuelNotFound)

	var got *apperr.Error
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As failed to extract *apperr.Error")
	}
	if got.Code != apperr.CodeDuelNotFound {
		t.Errorf("got.Code = %q, want %q", got.Code, apperr.CodeDuelNotFound)
	}
	if !errors.Is(got.Unwrap(), cause) {
		t.Errorf("Unwrap did not return cause")
	}
}

func TestWrap_NilApp_ReturnsNil(t *testing.T) {
	t.Parallel()

	got := apperr.Wrap(errors.New("x"), nil)
	if got != nil {
		t.Errorf("Wrap(_, nil) = %v, want nil", got)
	}
}

func TestWrap_NilCause_StillProducesError(t *testing.T) {
	t.Parallel()

	wrapped := apperr.Wrap(nil, apperr.ErrTaskValidation)
	if wrapped == nil {
		t.Fatal("Wrap(nil, app) returned nil")
	}
	if !errors.Is(wrapped, apperr.ErrTaskValidation) {
		t.Errorf("errors.Is(wrapped, ErrTaskValidation) = false")
	}
	if wrapped.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil", wrapped.Unwrap())
	}
}

func TestErrorString_FormatsWithCause(t *testing.T) {
	t.Parallel()

	bare := apperr.ErrInternal.Error()
	if bare != "internal error" {
		t.Errorf("bare Error() = %q, want %q", bare, "internal error")
	}

	wrapped := apperr.Wrap(errors.New("boom"), apperr.ErrInternal)
	want := "internal error: boom"
	if wrapped.Error() != want {
		t.Errorf("wrapped Error() = %q, want %q", wrapped.Error(), want)
	}
}

func TestErrorString_FmtErrorfChain(t *testing.T) {
	t.Parallel()

	cause := errors.New("low-level")
	app := apperr.Wrap(cause, apperr.ErrPlayerInDuel)
	outer := fmt.Errorf("usecase failed: %w", app)

	if !errors.Is(outer, apperr.ErrPlayerInDuel) {
		t.Errorf("errors.Is through fmt.Errorf chain failed")
	}
	if !errors.Is(outer, cause) {
		t.Errorf("errors.Is through fmt.Errorf to cause failed")
	}
}

func TestIs_DifferentCode_ReturnsFalse(t *testing.T) {
	t.Parallel()

	if errors.Is(apperr.ErrPlayerNotFound, apperr.ErrTaskNotFound) {
		t.Errorf("errors.Is(ErrPlayerNotFound, ErrTaskNotFound) = true, want false")
	}
	if errors.Is(apperr.ErrInternal, apperr.ErrValidation) {
		t.Errorf("errors.Is(ErrInternal, ErrValidation) = true, want false")
	}
}

func TestIs_NonAppError_ReturnsFalse(t *testing.T) {
	t.Parallel()

	plain := errors.New("not an apperr")
	if errors.Is(plain, apperr.ErrInternal) {
		t.Errorf("errors.Is(plainErr, apperrSentinel) = true, want false")
	}
}
