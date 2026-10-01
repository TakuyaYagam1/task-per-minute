package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChangePasswordVerifiesCurrentPasswordAndRotatesSession(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	oldToken := uuid.New()
	repo := newAccountSettingsRepositoryFake(playerID, oldToken, now)
	service, clock, _ := newAccountSettingsUseCaseFake(t, repo, &accountSettingsMailerFake{}, now)

	_, err := service.ChangePassword(context.Background(), playerID, oldToken, inbound.ChangePasswordCommand{
		CurrentPassword: "wrong passphrase",
		NewPassword:     "New-passphrase-983!",
	})
	require.ErrorIs(t, err, domain.ErrCurrentPasswordInvalid)
	require.Equal(t, "hash$correct passphrase", repo.record.PasswordHash)
	require.Equal(t, oldToken, *repo.record.Player.SessionToken)

	updated, err := service.ChangePassword(context.Background(), playerID, oldToken, inbound.ChangePasswordCommand{
		CurrentPassword: "correct passphrase",
		NewPassword:     "New-passphrase-983!",
	})
	require.NoError(t, err)
	require.NotNil(t, updated.SessionToken)
	require.NotEqual(t, oldToken, *updated.SessionToken)
	require.Equal(t, "hash$New-passphrase-983!", repo.record.PasswordHash)
	require.Equal(t, *updated.SessionToken, *repo.record.Player.SessionToken)
	require.WithinDuration(t, clock.now.Add(30*time.Minute), *updated.SessionExpiresAt, time.Second)
}

func TestChangeUsernameInvalidatesLeaderboardOnlyAfterCommit(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	token := uuid.New()
	repo := newAccountSettingsRepositoryFake(playerID, token, now)
	service, _, leaderboard := newAccountSettingsUseCaseFake(t, repo, &accountSettingsMailerFake{}, now)

	_, err := service.ChangeUsername(context.Background(), playerID, token, inbound.ChangeUsernameCommand{
		CurrentPassword: "wrong passphrase",
		Username:        "alice2",
	})
	require.ErrorIs(t, err, domain.ErrCurrentPasswordInvalid)
	require.Zero(t, leaderboard.calls)

	repo.changeUsernameErr = domain.ErrUsernameTaken
	_, err = service.ChangeUsername(context.Background(), playerID, token, inbound.ChangeUsernameCommand{
		CurrentPassword: "correct passphrase",
		Username:        "alice2",
	})
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
	require.Zero(t, leaderboard.calls)

	repo.changeUsernameErr = nil
	updated, err := service.ChangeUsername(context.Background(), playerID, token, inbound.ChangeUsernameCommand{
		CurrentPassword: "correct passphrase",
		Username:        "alice2",
	})
	require.NoError(t, err)
	require.Equal(t, "alice2", updated.Username)
	require.Equal(t, 1, leaderboard.calls)
}

func TestEmailChangeAttemptsPersistAcrossCancelAndResend(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	token := uuid.New()
	repo := newAccountSettingsRepositoryFake(playerID, token, now)
	mailer := &accountSettingsMailerFake{}
	service, clock, _ := newAccountSettingsUseCaseFake(t, repo, mailer, now)

	settings, err := service.BeginEmailChange(context.Background(), playerID, token, inbound.BeginEmailChangeCommand{
		CurrentPassword: "correct passphrase",
		NewEmail:        "first.new@example.test",
	})
	require.NoError(t, err)
	require.Equal(t, "alice@example.test", settings.Email)
	require.Equal(t, "first.new@example.test", *settings.PendingEmail)
	require.Len(t, mailer.codes, 1)

	wrongCode := differentCode(mailer.codes[0])
	_, err = service.ConfirmEmailChange(context.Background(), playerID, token, inbound.ConfirmEmailChangeCommand{Code: wrongCode})
	require.ErrorIs(t, err, domain.ErrEmailChangeCodeInvalid)
	require.EqualValues(t, 1, repo.record.EmailChangeAttemptCount)

	settings, err = service.CancelEmailChange(context.Background(), playerID, token)
	require.NoError(t, err)
	require.Nil(t, settings.PendingEmail)
	require.EqualValues(t, 1, repo.record.EmailChangeAttemptCount)
	require.EqualValues(t, 1, repo.record.EmailChangeSendCount)
	clock.now = clock.now.Add(emailChangeCooldown)

	_, err = service.BeginEmailChange(context.Background(), playerID, token, inbound.BeginEmailChangeCommand{
		CurrentPassword: "correct passphrase",
		NewEmail:        "second.new@example.test",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, repo.record.EmailChangeAttemptCount)
	require.EqualValues(t, 2, repo.record.EmailChangeSendCount)

	_, err = service.ResendEmailChange(context.Background(), playerID, token)
	require.ErrorIs(t, err, domain.ErrEmailChangeRateLimited)
	require.Equal(t, time.Minute, retryAfterDuration(t, err))

	clock.now = clock.now.Add(emailChangeCooldown)
	_, err = service.ResendEmailChange(context.Background(), playerID, token)
	require.NoError(t, err)
	require.EqualValues(t, 3, repo.record.EmailChangeSendCount)
	require.Len(t, mailer.codes, 3)

	for attempt := 2; attempt <= int(maxEmailChangeAttempts); attempt++ {
		_, err = service.ConfirmEmailChange(context.Background(), playerID, token, inbound.ConfirmEmailChangeCommand{
			Code: differentCode(mailer.codes[len(mailer.codes)-1]),
		})
		if attempt < int(maxEmailChangeAttempts) {
			require.ErrorIs(t, err, domain.ErrEmailChangeCodeInvalid)
		} else {
			require.ErrorIs(t, err, domain.ErrEmailChangeAttemptsExceeded)
			require.Equal(t, time.Hour-2*emailChangeCooldown, retryAfterDuration(t, err))
		}
		require.EqualValues(t, attempt, repo.record.EmailChangeAttemptCount)
	}
}

func TestEmailChangeDeliveryFailureLeavesChallengeAndCooldown(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	token := uuid.New()
	repo := newAccountSettingsRepositoryFake(playerID, token, now)
	mailer := &accountSettingsMailerFake{codeErr: errors.New("provider unavailable")}
	service, _, _ := newAccountSettingsUseCaseFake(t, repo, mailer, now)

	_, err := service.BeginEmailChange(context.Background(), playerID, token, inbound.BeginEmailChangeCommand{
		CurrentPassword: "correct passphrase",
		NewEmail:        "next@example.test",
	})
	require.ErrorIs(t, err, domain.ErrEmailChangeUnavailable)
	require.Equal(t, "next@example.test", *repo.record.PendingEmail)
	require.EqualValues(t, 1, repo.record.EmailChangeSendCount)
	require.NotNil(t, repo.record.EmailChangeLastSentAt)

	_, err = service.ResendEmailChange(context.Background(), playerID, token)
	require.ErrorIs(t, err, domain.ErrEmailChangeRateLimited)
	require.Equal(t, time.Minute, retryAfterDuration(t, err))
}

func TestConfirmEmailChangeRotatesSessionAndReportsNoticeFailure(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	token := uuid.New()
	repo := newAccountSettingsRepositoryFake(playerID, token, now)
	mailer := &accountSettingsMailerFake{noticeErr: errors.New("provider unavailable")}
	service, _, _ := newAccountSettingsUseCaseFake(t, repo, mailer, now)

	_, err := service.BeginEmailChange(context.Background(), playerID, token, inbound.BeginEmailChangeCommand{
		CurrentPassword: "correct passphrase",
		NewEmail:        "new@example.test",
	})
	require.NoError(t, err)

	result, err := service.ConfirmEmailChange(context.Background(), playerID, token, inbound.ConfirmEmailChangeCommand{
		Code: mailer.codes[0],
	})
	require.NoError(t, err)
	require.Equal(t, "new@example.test", result.Email)
	require.False(t, result.PreviousEmailNotified)
	require.Equal(t, "alice@example.test", mailer.notifiedPrevious)
	require.Equal(t, "new@example.test", mailer.notifiedNew)
	require.Equal(t, "new@example.test", repo.record.CurrentEmail)
	require.Nil(t, repo.record.PendingEmail)
	require.NotNil(t, result.Player.SessionToken)
	require.NotEqual(t, token, *result.Player.SessionToken)
	require.Equal(t, *result.Player.SessionToken, *repo.record.Player.SessionToken)
}

func newAccountSettingsUseCaseFake(
	t *testing.T,
	repo *accountSettingsRepositoryFake,
	mailer *accountSettingsMailerFake,
	now time.Time,
) (*AccountSettingsUseCase, *mutableAccountClock, *settingsLeaderboardFake) {
	t.Helper()
	hasher := &settingsHasherFake{}
	clock := &mutableAccountClock{now: now}
	leaderboard := &settingsLeaderboardFake{}
	service, err := NewAccountSettingsUseCase(SettingsConfig{SessionTTL: 30 * time.Minute}, inlineTransaction{}, repo, hasher, mailer, leaderboard, clock)
	require.NoError(t, err)
	return service, clock, leaderboard
}

func newAccountSettingsRepositoryFake(playerID, token uuid.UUID, now time.Time) *accountSettingsRepositoryFake {
	expiresAt := now.Add(time.Hour)
	return &accountSettingsRepositoryFake{record: &AccountSettingsRecord{
		AccountID:          uuid.New(),
		Player:             &domain.Player{ID: playerID, Username: "alice", SessionToken: &token, SessionExpiresAt: &expiresAt},
		UsernameNormalized: "alice",
		CurrentEmail:       "alice@example.test",
		EmailNormalized:    "alice@example.test",
		PasswordHash:       "hash$correct passphrase",
	}}
}

type settingsHasherFake struct{}

func (settingsHasherFake) Hash(value string) (string, error) { return "hash$" + value, nil }

func (settingsHasherFake) Verify(value, encoded string) (bool, error) {
	return encoded == "hash$"+value, nil
}

type accountSettingsMailerFake struct {
	codes            []string
	codeErr          error
	noticeErr        error
	notifiedPrevious string
	notifiedNew      string
}

type settingsLeaderboardFake struct {
	calls int
}

func (l *settingsLeaderboardFake) Invalidate() { l.calls++ }

func (m *accountSettingsMailerFake) SendEmailChangeCode(_ context.Context, _, code string) error {
	m.codes = append(m.codes, code)
	return m.codeErr
}

func (m *accountSettingsMailerFake) SendEmailChangedNotice(_ context.Context, previousEmail, newEmail string) error {
	m.notifiedPrevious = previousEmail
	m.notifiedNew = newEmail
	return m.noticeErr
}

type accountSettingsRepositoryFake struct {
	record            *AccountSettingsRecord
	changeUsernameErr error
}

func (r *accountSettingsRepositoryFake) LockAccountSettings(_ context.Context, playerID, token uuid.UUID, now time.Time) (*AccountSettingsRecord, error) {
	if r.record == nil || r.record.Player == nil || r.record.Player.ID != playerID || r.record.Player.SessionToken == nil || *r.record.Player.SessionToken != token || r.record.Player.SessionExpiresAt == nil || !r.record.Player.SessionExpiresAt.After(now) {
		return nil, domain.ErrInvalidSession
	}
	return r.record, nil
}

func (r *accountSettingsRepositoryFake) ChangeAccountUsername(_ context.Context, record *AccountSettingsRecord, username, normalized string) error {
	if r.changeUsernameErr != nil {
		return r.changeUsernameErr
	}
	record.Player.Username = username
	record.UsernameNormalized = normalized
	return nil
}

func (r *accountSettingsRepositoryFake) ChangeAccountPassword(_ context.Context, record *AccountSettingsRecord, passwordHash string, sessionToken, newSessionToken uuid.UUID, _ time.Time, expiresAt time.Time) (*domain.Player, error) {
	if record.Player.SessionToken == nil || *record.Player.SessionToken != sessionToken {
		return nil, domain.ErrInvalidSession
	}
	record.PasswordHash = passwordHash
	record.Player.SessionToken = &newSessionToken
	record.Player.SessionExpiresAt = &expiresAt
	return cloneSettingsPlayer(record.Player), nil
}

func (r *accountSettingsRepositoryFake) StartEmailChange(_ context.Context, record *AccountSettingsRecord, email, normalized, codeHash string, expiresAt, sentAt, sendWindow time.Time, sendCount int32, attemptWindow time.Time, attemptCount int32) error {
	record.PendingEmail = &email
	record.PendingEmailNormalized = &normalized
	record.EmailChangeCodeHash = codeHash
	record.EmailChangeExpiresAt = &expiresAt
	record.EmailChangeLastSentAt = &sentAt
	record.EmailChangeSendWindowStartedAt = &sendWindow
	record.EmailChangeSendCount = sendCount
	record.EmailChangeAttemptWindowStartedAt = &attemptWindow
	record.EmailChangeAttemptCount = attemptCount
	return nil
}

func (r *accountSettingsRepositoryFake) RecordEmailChangeAttempt(_ context.Context, record *AccountSettingsRecord, window time.Time, count int32) error {
	record.EmailChangeAttemptWindowStartedAt = &window
	record.EmailChangeAttemptCount = count
	return nil
}

func (r *accountSettingsRepositoryFake) CancelEmailChange(_ context.Context, record *AccountSettingsRecord) error {
	record.PendingEmail = nil
	record.PendingEmailNormalized = nil
	record.EmailChangeCodeHash = ""
	record.EmailChangeExpiresAt = nil
	return nil
}

func (r *accountSettingsRepositoryFake) ConfirmEmailChange(_ context.Context, record *AccountSettingsRecord, sessionToken, newSessionToken uuid.UUID, _ time.Time, expiresAt time.Time) (*domain.Player, error) {
	if record.Player.SessionToken == nil || *record.Player.SessionToken != sessionToken || record.PendingEmail == nil || record.PendingEmailNormalized == nil {
		return nil, domain.ErrInvalidSession
	}
	record.CurrentEmail = *record.PendingEmail
	record.EmailNormalized = *record.PendingEmailNormalized
	record.PendingEmail = nil
	record.PendingEmailNormalized = nil
	record.EmailChangeCodeHash = ""
	record.EmailChangeExpiresAt = nil
	record.Player.SessionToken = &newSessionToken
	record.Player.SessionExpiresAt = &expiresAt
	return cloneSettingsPlayer(record.Player), nil
}

func cloneSettingsPlayer(player *domain.Player) *domain.Player {
	result := *player
	if player.SessionToken != nil {
		token := *player.SessionToken
		result.SessionToken = &token
	}
	if player.SessionExpiresAt != nil {
		expiresAt := *player.SessionExpiresAt
		result.SessionExpiresAt = &expiresAt
	}
	return &result
}

func differentCode(value string) string {
	lastDigit := value[len(value)-1]
	if lastDigit == '0' {
		return value[:len(value)-1] + "1"
	}
	return value[:len(value)-1] + "0"
}

func retryAfterDuration(t *testing.T, err error) time.Duration {
	t.Helper()
	var retryAfter interface{ RetryAfter() time.Duration }
	require.ErrorAs(t, err, &retryAfter)
	return retryAfter.RetryAfter()
}

var _ SettingsRepository = (*accountSettingsRepositoryFake)(nil)
var _ AccountSettingsMailer = (*accountSettingsMailerFake)(nil)
var _ playerusecase.LeaderboardInvalidator = (*settingsLeaderboardFake)(nil)
