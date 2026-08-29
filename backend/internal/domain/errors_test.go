package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestApplicationErrorSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *domain.Error
		code domain.ErrorCode
	}{
		{"player_not_found", domain.ErrPlayerNotFound, domain.ErrorCodePlayerNotFound},
		{"username_taken", domain.ErrUsernameTaken, domain.ErrorCodeUsernameTaken},
		{"username_invalid", domain.ErrUsernameInvalid, domain.ErrorCodeUsernameInvalid},
		{"player_in_duel", domain.ErrPlayerInDuel, domain.ErrorCodePlayerInDuel},
		{"player_queued", domain.ErrPlayerQueued, domain.ErrorCodePlayerQueued},
		{"player_reserved", domain.ErrPlayerReserved, domain.ErrorCodePlayerReserved},
		{"invalid_session", domain.ErrInvalidSession, domain.ErrorCodeInvalidSession},
		{"task_not_found", domain.ErrTaskNotFound, domain.ErrorCodeTaskNotFound},
		{"task_in_use", domain.ErrTaskInUse, domain.ErrorCodeTaskInUse},
		{"task_validation", domain.ErrTaskValidation, domain.ErrorCodeTaskValidation},
		{"duel_not_found", domain.ErrDuelNotFound, domain.ErrorCodeDuelNotFound},
		{"duel_finished", domain.ErrDuelFinished, domain.ErrorCodeDuelFinished},
		{"duel_deadline_passed", domain.ErrDuelDeadlinePassed, domain.ErrorCodeDuelDeadlinePassed},
		{"flag_incorrect", domain.ErrFlagIncorrect, domain.ErrorCodeFlagIncorrect},
		{"not_duel_participant", domain.ErrNotDuelParticipant, domain.ErrorCodeNotDuelParticipant},
		{"invalid_credentials", domain.ErrInvalidCredentials, domain.ErrorCodeInvalidCredentials},
		{"token_expired", domain.ErrTokenExpired, domain.ErrorCodeTokenExpired},
		{"token_revoked", domain.ErrTokenRevoked, domain.ErrorCodeTokenRevoked},
		{"internal", domain.ErrInternal, domain.ErrorCodeInternal},
		{"validation", domain.ErrValidation, domain.ErrorCodeValidation},
		{"conflict", domain.ErrConflict, domain.ErrorCodeConflict},
		{"rate_limited", domain.ErrRateLimited, domain.ErrorCodeRateLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.err.Code != tt.code {
				t.Errorf("Code = %q, want %q", tt.err.Code, tt.code)
			}
			if tt.err.Error() == "" {
				t.Error("Error() returned empty string")
			}
			if !errors.Is(tt.err, tt.err) {
				t.Error("errors.Is(self, self) = false, want true")
			}
		})
	}
}

func TestWrapErrorPreservesIdentity(t *testing.T) {
	t.Parallel()

	cause := errors.New("pgx: no rows")
	wrapped := domain.WrapError(cause, domain.ErrPlayerNotFound)

	if !errors.Is(wrapped, domain.ErrPlayerNotFound) {
		t.Error("errors.Is(wrapped, ErrPlayerNotFound) = false, want true")
	}
	if errors.Is(wrapped, domain.ErrTaskNotFound) {
		t.Error("errors.Is(wrapped, ErrTaskNotFound) = true, want false")
	}
	if !errors.Is(wrapped, cause) {
		t.Error("errors.Is(wrapped, cause) = false, want true")
	}
}

func TestWrapErrorCanBeExtracted(t *testing.T) {
	t.Parallel()

	cause := errors.New("pgx: no rows")
	wrapped := domain.WrapError(cause, domain.ErrDuelNotFound)

	var got *domain.Error
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As failed to extract *domain.Error")
	}
	if got.Code != domain.ErrorCodeDuelNotFound {
		t.Errorf("got.Code = %q, want %q", got.Code, domain.ErrorCodeDuelNotFound)
	}
	if !errors.Is(got.Unwrap(), cause) {
		t.Error("Unwrap did not return cause")
	}
}

func TestWrapErrorWithNilDomainErrorReturnsNil(t *testing.T) {
	t.Parallel()

	if got := domain.WrapError(errors.New("x"), nil); got != nil {
		t.Errorf("WrapError(_, nil) = %v, want nil", got)
	}
}

func TestWrapErrorWithNilCauseStillProducesError(t *testing.T) {
	t.Parallel()

	wrapped := domain.WrapError(nil, domain.ErrTaskValidation)
	if wrapped == nil {
		t.Fatal("WrapError(nil, domainErr) returned nil")
	}
	if !errors.Is(wrapped, domain.ErrTaskValidation) {
		t.Error("errors.Is(wrapped, ErrTaskValidation) = false")
	}
	if wrapped.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil", wrapped.Unwrap())
	}
}

func TestApplicationErrorStringIncludesCause(t *testing.T) {
	t.Parallel()

	if got := domain.ErrInternal.Error(); got != "internal error" {
		t.Errorf("bare Error() = %q, want %q", got, "internal error")
	}

	wrapped := domain.WrapError(errors.New("boom"), domain.ErrInternal)
	if got, want := wrapped.Error(), "internal error: boom"; got != want {
		t.Errorf("wrapped Error() = %q, want %q", got, want)
	}
}

func TestApplicationErrorIdentitySurvivesWrapping(t *testing.T) {
	t.Parallel()

	cause := errors.New("low-level")
	domainErr := domain.WrapError(cause, domain.ErrPlayerInDuel)
	outer := fmt.Errorf("usecase failed: %w", domainErr)

	if !errors.Is(outer, domain.ErrPlayerInDuel) {
		t.Error("errors.Is through fmt.Errorf chain failed")
	}
	if !errors.Is(outer, cause) {
		t.Error("errors.Is through fmt.Errorf to cause failed")
	}
}

func TestApplicationErrorIdentityUsesCode(t *testing.T) {
	t.Parallel()

	if errors.Is(domain.ErrPlayerNotFound, domain.ErrTaskNotFound) {
		t.Error("different domain error codes matched")
	}
	if errors.Is(domain.ErrInternal, domain.ErrValidation) {
		t.Error("different generic error codes matched")
	}
	if errors.Is(errors.New("plain error"), domain.ErrInternal) {
		t.Error("plain error matched a domain error")
	}
}
