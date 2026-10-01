//go:build integration

package player_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	accountrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/account"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	accountusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/account"
)

func TestAccountSettingsRenameAndPasswordChangeKeepPlayerIdentity(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	ctx := context.Background()
	settings, login, repository, mgr, clock, player, token, _ := newSettingsIntegrationFixture(t, pool, uniq("settings_user"))
	credentials, err := repository.FindLoginCredentials(ctx, strings.ToLower(player.Username))
	require.NoError(t, err)
	require.NotNil(t, credentials)
	reservedUsername := uniq("reserved_user")
	_, _ = createVerifiedAccountSession(ctx, t, pool, mgr, playerrepo.NewPlayerPostgres(mgr), reservedUsername, clock.Now().Add(time.Hour))
	_, err = settings.ChangeUsername(ctx, player.ID, token, inbound.ChangeUsernameCommand{
		CurrentPassword: accountTestPassword,
		Username:        reservedUsername,
	})
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
	var unchangedUsername string
	require.NoError(t, pool.QueryRow(ctx, `SELECT username FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&unchangedUsername))
	require.Equal(t, player.Username, unchangedUsername)

	_, err = settings.ChangeUsername(ctx, player.ID, token, inbound.ChangeUsernameCommand{
		CurrentPassword: "incorrect passphrase",
		Username:        uniq("renamed_user"),
	})
	require.ErrorIs(t, err, domain.ErrCurrentPasswordInvalid)

	newUsername := uniq("renamed_user")
	renamed, err := settings.ChangeUsername(ctx, player.ID, token, inbound.ChangeUsernameCommand{
		CurrentPassword: accountTestPassword,
		Username:        newUsername,
	})
	require.NoError(t, err)
	require.Equal(t, player.ID, renamed.ID)
	require.Equal(t, newUsername, renamed.Username)
	var accountUsername, playerUsername string
	require.NoError(t, pool.QueryRow(ctx, `SELECT username FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&accountUsername))
	require.NoError(t, pool.QueryRow(ctx, `SELECT username FROM players WHERE id = $1`, player.ID).Scan(&playerUsername))
	require.Equal(t, newUsername, accountUsername)
	require.Equal(t, newUsername, playerUsername)

	staleToken := uuid.New()
	err = mgr.Do(ctx, func(txCtx context.Context) error {
		_, updateErr := repository.UpdateAccountPlayerSession(
			txCtx,
			player.ID,
			credentials.UsernameNormalized,
			credentials.PasswordHash,
			staleToken,
			clock.Now().Add(time.Hour),
		)
		return updateErr
	})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials, "a login verified under the old username must not rotate a session after rename")

	newPassword := "Changed-password-283!"
	changed, err := settings.ChangePassword(ctx, player.ID, token, inbound.ChangePasswordCommand{
		CurrentPassword: accountTestPassword,
		NewPassword:     newPassword,
	})
	require.NoError(t, err)
	require.Equal(t, player.ID, changed.ID)
	require.NotNil(t, changed.SessionToken)
	require.NotEqual(t, token, *changed.SessionToken)
	_, err = settings.GetAccountSettings(ctx, player.ID, token)
	require.ErrorIs(t, err, domain.ErrInvalidSession)
	accountSettings, err := settings.GetAccountSettings(ctx, player.ID, *changed.SessionToken)
	require.NoError(t, err)
	require.Equal(t, newUsername, accountSettings.Username)

	err = mgr.Do(ctx, func(txCtx context.Context) error {
		_, updateErr := repository.UpdateAccountPlayerSession(
			txCtx,
			player.ID,
			strings.ToLower(newUsername),
			credentials.PasswordHash,
			staleToken,
			clock.Now().Add(time.Hour),
		)
		return updateErr
	})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials, "a login verified under the old password must not rotate a session after password change")
	_, err = login.Login(ctx, inbound.LoginPlayerCommand{Login: newUsername, Password: accountTestPassword})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	loggedIn, err := login.Login(ctx, inbound.LoginPlayerCommand{Login: newUsername, Password: newPassword})
	require.NoError(t, err)
	require.Equal(t, player.ID, loggedIn.ID)
}

func TestAccountSettingsEmailChangePersistsAttemptBudgetAndRotatesSession(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	ctx := context.Background()
	settings, login, _, _, clock, player, token, mailer := newSettingsIntegrationFixture(t, pool, uniq("email_settings"))
	var currentEmail string
	require.NoError(t, pool.QueryRow(ctx, `SELECT email FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&currentEmail))

	firstEmail := uniq("first_new") + "@example.test"
	settingsState, err := settings.BeginEmailChange(ctx, player.ID, token, inbound.BeginEmailChangeCommand{
		CurrentPassword: accountTestPassword,
		NewEmail:        firstEmail,
	})
	require.NoError(t, err)
	require.Equal(t, currentEmail, settingsState.Email)
	require.Equal(t, firstEmail, *settingsState.PendingEmail)
	firstCode := mailer.Sent()[0].code
	var storedEmail string
	var sendCount int32
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT email, email_change_send_count
		FROM player_accounts
		WHERE player_id = $1`, player.ID).Scan(&storedEmail, &sendCount))
	require.Equal(t, currentEmail, storedEmail, "the current email remains active until confirmation")
	require.EqualValues(t, 1, sendCount)

	_, err = settings.ConfirmEmailChange(ctx, player.ID, token, inbound.ConfirmEmailChangeCommand{Code: differentIntegrationCode(firstCode)})
	require.ErrorIs(t, err, domain.ErrEmailChangeCodeInvalid)
	var attempts int32
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_change_attempt_count FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&attempts))
	require.EqualValues(t, 1, attempts, "a rejected code attempt must commit")

	settingsState, err = settings.CancelEmailChange(ctx, player.ID, token)
	require.NoError(t, err)
	require.Nil(t, settingsState.PendingEmail)
	var pendingEmail *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT pending_email FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&pendingEmail))
	require.Nil(t, pendingEmail)
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_change_attempt_count, email_change_send_count FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&attempts, &sendCount))
	require.EqualValues(t, 1, attempts)
	require.EqualValues(t, 1, sendCount)

	clock.Advance(time.Minute)
	secondEmail := uniq("second_new") + "@example.test"
	_, err = settings.BeginEmailChange(ctx, player.ID, token, inbound.BeginEmailChangeCommand{
		CurrentPassword: accountTestPassword,
		NewEmail:        secondEmail,
	})
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_change_attempt_count, email_change_send_count FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&attempts, &sendCount))
	require.EqualValues(t, 1, attempts, "cancelling and restarting must preserve the attempt budget")
	require.EqualValues(t, 2, sendCount)
	secondCode := mailer.Sent()[1].code
	for expected := int32(2); expected <= 5; expected++ {
		_, err = settings.ConfirmEmailChange(ctx, player.ID, token, inbound.ConfirmEmailChangeCommand{Code: differentIntegrationCode(secondCode)})
		if expected < 5 {
			require.ErrorIs(t, err, domain.ErrEmailChangeCodeInvalid)
		} else {
			require.ErrorIs(t, err, domain.ErrEmailChangeAttemptsExceeded)
		}
		require.NoError(t, pool.QueryRow(ctx, `SELECT email_change_attempt_count FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&attempts))
		require.Equal(t, expected, attempts)
	}

	clock.Advance(time.Hour)
	_, err = settings.ResendEmailChange(ctx, player.ID, token)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_change_attempt_count, email_change_send_count FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&attempts, &sendCount))
	require.Zero(t, attempts, "the attempt budget becomes available after its one-hour window")
	require.EqualValues(t, 1, sendCount, "send count resets after the hourly window")
	latestCode := mailer.Sent()[2].code
	confirmed, err := settings.ConfirmEmailChange(ctx, player.ID, token, inbound.ConfirmEmailChangeCommand{Code: latestCode})
	require.NoError(t, err)
	require.Equal(t, secondEmail, confirmed.Email)
	require.True(t, confirmed.PreviousEmailNotified)
	require.Equal(t, player.ID, confirmed.Player.ID)
	require.NotNil(t, confirmed.Player.SessionToken)
	require.NotEqual(t, token, *confirmed.Player.SessionToken)
	require.Equal(t, currentEmail, mailer.NotifiedPrevious())
	var finalEmail string
	require.NoError(t, pool.QueryRow(ctx, `SELECT email FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&finalEmail))
	require.Equal(t, secondEmail, finalEmail)
	_, err = settings.GetAccountSettings(ctx, player.ID, token)
	require.ErrorIs(t, err, domain.ErrInvalidSession)

	_, err = settings.ConfirmEmailChange(ctx, player.ID, *confirmed.Player.SessionToken, inbound.ConfirmEmailChangeCommand{Code: latestCode})
	require.ErrorIs(t, err, domain.ErrEmailChangeCodeInvalid, "a confirmed code cannot be replayed")
	_, err = login.Login(ctx, inbound.LoginPlayerCommand{Login: currentEmail, Password: accountTestPassword})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	newLogin, err := login.Login(ctx, inbound.LoginPlayerCommand{Login: secondEmail, Password: accountTestPassword})
	require.NoError(t, err)
	require.Equal(t, player.ID, newLogin.ID)
	require.NotNil(t, newLogin.SessionToken)
	clock.Advance(time.Minute)
	thirdEmail := uniq("expired_new") + "@example.test"
	_, err = settings.BeginEmailChange(ctx, player.ID, *newLogin.SessionToken, inbound.BeginEmailChangeCommand{
		CurrentPassword: accountTestPassword,
		NewEmail:        thirdEmail,
	})
	require.NoError(t, err)
	expiredCode := mailer.Sent()[3].code
	clock.Advance(10*time.Minute + time.Second)
	_, err = settings.ConfirmEmailChange(ctx, player.ID, *newLogin.SessionToken, inbound.ConfirmEmailChangeCommand{Code: expiredCode})
	require.ErrorIs(t, err, domain.ErrEmailChangeCodeInvalid)
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_change_attempt_count FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&attempts))
	require.EqualValues(t, 1, attempts, "an expired code is invalid and consumes a persisted attempt")
}

func TestAccountSettingsEmailCollisionIsAtomic(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	ctx := context.Background()
	settingsA, _, _, _, _, playerA, tokenA, mailerA := newSettingsIntegrationFixture(t, pool, uniq("email_a"))
	settingsB, _, _, _, _, playerB, tokenB, mailerB := newSettingsIntegrationFixture(t, pool, uniq("email_b"))
	sharedEmail := uniq("shared_pending") + "@example.test"

	_, err := settingsA.BeginEmailChange(ctx, playerA.ID, tokenA, inbound.BeginEmailChangeCommand{
		CurrentPassword: accountTestPassword,
		NewEmail:        sharedEmail,
	})
	require.NoError(t, err)
	_, err = settingsB.BeginEmailChange(ctx, playerB.ID, tokenB, inbound.BeginEmailChangeCommand{
		CurrentPassword: accountTestPassword,
		NewEmail:        sharedEmail,
	})
	require.NoError(t, err, "a pending address does not reserve the address")

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, attempt := range []struct {
		service *accountusecase.AccountSettingsUseCase
		player  *domain.Player
		token   uuid.UUID
		code    string
	}{
		{service: settingsA, player: playerA, token: tokenA, code: mailerA.Sent()[0].code},
		{service: settingsB, player: playerB, token: tokenB, code: mailerB.Sent()[0].code},
	} {
		go func(attempt struct {
			service *accountusecase.AccountSettingsUseCase
			player  *domain.Player
			token   uuid.UUID
			code    string
		}) {
			<-start
			_, confirmErr := attempt.service.ConfirmEmailChange(ctx, attempt.player.ID, attempt.token, inbound.ConfirmEmailChangeCommand{Code: attempt.code})
			results <- confirmErr
		}(attempt)
	}
	close(start)
	one := <-results
	two := <-results
	if one == nil {
		require.ErrorIs(t, two, domain.ErrEmailTaken)
	} else {
		require.ErrorIs(t, one, domain.ErrEmailTaken)
		require.NoError(t, two)
	}
	var emailCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM player_accounts WHERE email_normalized = lower($1)`, sharedEmail).Scan(&emailCount))
	require.Equal(t, 1, emailCount)
}

func TestAccountSettingsMailFailureKeepsPersistedCooldown(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	ctx := context.Background()
	settings, _, _, _, _, player, token, mailer := newSettingsIntegrationFixture(t, pool, uniq("mail_failure"))
	mailer.codeErr = errors.New("synthetic provider failure")

	_, err := settings.BeginEmailChange(ctx, player.ID, token, inbound.BeginEmailChangeCommand{
		CurrentPassword: accountTestPassword,
		NewEmail:        uniq("mail_target") + "@example.test",
	})
	require.ErrorIs(t, err, domain.ErrEmailChangeUnavailable)
	var pendingEmail *string
	var sends int32
	require.NoError(t, pool.QueryRow(ctx, `SELECT pending_email, email_change_send_count FROM player_accounts WHERE player_id = $1`, player.ID).Scan(&pendingEmail, &sends))
	require.NotNil(t, pendingEmail)
	require.EqualValues(t, 1, sends)

	_, err = settings.ResendEmailChange(ctx, player.ID, token)
	require.ErrorIs(t, err, domain.ErrEmailChangeRateLimited)
	var retryAfter interface{ RetryAfter() time.Duration }
	require.ErrorAs(t, err, &retryAfter)
	require.InDelta(t, float64(time.Minute), float64(retryAfter.RetryAfter()), float64(time.Millisecond))
}

type settingsIntegrationMail struct {
	recipient string
	code      string
}

type settingsIntegrationMailer struct {
	mu                sync.Mutex
	codes             []settingsIntegrationMail
	previousEmail     string
	newEmail          string
	codeErr           error
	notificationError error
}

func (m *settingsIntegrationMailer) SendEmailChangeCode(_ context.Context, recipient, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes = append(m.codes, settingsIntegrationMail{recipient: recipient, code: code})
	return m.codeErr
}

func (m *settingsIntegrationMailer) SendEmailChangedNotice(_ context.Context, previousEmail, newEmail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.previousEmail = previousEmail
	m.newEmail = newEmail
	return m.notificationError
}

func (m *settingsIntegrationMailer) Sent() []settingsIntegrationMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]settingsIntegrationMail(nil), m.codes...)
}

func (m *settingsIntegrationMailer) NotifiedPrevious() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.previousEmail
}

type settingsIntegrationLeaderboard struct{}

func (settingsIntegrationLeaderboard) Invalidate() {}

func newSettingsIntegrationFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	username string,
) (
	*accountusecase.AccountSettingsUseCase,
	*accountusecase.UseCase,
	*accountrepo.AccountPostgres,
	*postgres.TxManager,
	*accountTestClock,
	*domain.Player,
	uuid.UUID,
	*settingsIntegrationMailer,
) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	clock := &accountTestClock{now: now}
	mgr := postgres.NewTxManager(pool)
	expiresAt := now.Add(2 * time.Hour)
	player, token := createVerifiedAccountSession(ctx, t, pool, mgr, playerrepo.NewPlayerPostgres(mgr), username, expiresAt)
	passwords := auth.NewPasswordHasher()
	passwordHash, err := passwords.Hash(accountTestPassword)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE player_accounts SET password_hash = $2 WHERE player_id = $1`, player.ID, passwordHash)
	require.NoError(t, err)

	repository := accountrepo.NewAccountPostgres(mgr)
	verificationMailer := &accountTestMailer{}
	login, err := accountusecase.NewUseCase(accountusecase.Config{SessionTTL: time.Hour}, mgr, repository, passwords, verificationMailer, clock)
	require.NoError(t, err)
	settingsMailer := &settingsIntegrationMailer{}
	settings, err := accountusecase.NewAccountSettingsUseCase(
		accountusecase.SettingsConfig{SessionTTL: time.Hour},
		mgr,
		repository,
		passwords,
		settingsMailer,
		settingsIntegrationLeaderboard{},
		clock,
	)
	require.NoError(t, err)
	return settings, login, repository, mgr, clock, player, token, settingsMailer
}

func differentIntegrationCode(value string) string {
	lastDigit := value[len(value)-1]
	if lastDigit == '0' {
		return value[:len(value)-1] + "1"
	}
	return value[:len(value)-1] + "0"
}
