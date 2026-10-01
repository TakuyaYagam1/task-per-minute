//go:build integration

package player_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

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

const (
	verificationPageURL        = "https://ctfleague.example.test/verify-email"
	accountTestPassword        = "Lifecycle-test-password-284"
	accountReplacementPassword = "Another-test-password-938"
)

type accountTestClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *accountTestClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *accountTestClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

type capturedVerification struct {
	recipient string
	url       string
}

type accountTestMailer struct {
	mu      sync.Mutex
	entries []capturedVerification
}

func (m *accountTestMailer) SendVerification(_ context.Context, recipient, verificationURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, capturedVerification{recipient: recipient, url: verificationURL})
	return nil
}

func (m *accountTestMailer) Entries() []capturedVerification {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]capturedVerification(nil), m.entries...)
}

func newAccountLifecycleUseCase(
	mgr *postgres.TxManager,
	clock *accountTestClock,
	mailer *accountTestMailer,
) *accountusecase.UseCase {
	uc, err := accountusecase.NewUseCase(
		accountusecase.Config{VerificationPageURL: verificationPageURL},
		mgr,
		accountrepo.NewAccountPostgres(mgr),
		auth.NewPasswordHasher(),
		mailer,
		clock,
	)
	if err != nil {
		panic("construct account use case: " + err.Error())
	}
	return uc
}

func newAccountLifecycleFixture(t *testing.T, pool *pgxpool.Pool) (*accountusecase.UseCase, *playerrepo.PlayerPostgres, *accountTestClock, *accountTestMailer) {
	t.Helper()
	mgr := postgres.NewTxManager(pool)
	players := playerrepo.NewPlayerPostgres(mgr)
	clock := &accountTestClock{now: time.Now().UTC()}
	mailer := &accountTestMailer{}
	uc := newAccountLifecycleUseCase(mgr, clock, mailer)
	return uc, players, clock, mailer
}

func verificationTokenFromMail(tb testing.TB, entry capturedVerification) string {
	tb.Helper()
	parsed, err := url.Parse(entry.url)
	require.NoError(tb, err)
	fragment, err := url.ParseQuery(parsed.Fragment)
	require.NoError(tb, err)
	tokens := fragment["token"]
	require.Len(tb, tokens, 1)
	return tokens[0]
}

func registerTestAccount(t *testing.T, uc *accountusecase.UseCase, username, email string) {
	t.Helper()
	err := uc.Register(context.Background(), inbound.RegisterPlayerCommand{
		Username: username,
		Email:    email,
		Password: accountTestPassword,
	})
	require.NoError(t, err)
}

func TestAccountRegisterConcurrentCaseInsensitiveUsernameReservation(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, _, _, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()
	baseName := uniq("race_name")
	commands := []inbound.RegisterPlayerCommand{
		{Username: baseName, Email: uniq("first") + "@example.test", Password: accountTestPassword},
		{Username: strings.ToUpper(baseName), Email: uniq("second") + "@example.test", Password: accountTestPassword},
	}
	start := make(chan struct{})
	type result struct{ err error }
	results := make(chan result, len(commands))
	var workers sync.WaitGroup
	for _, command := range commands {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- result{err: uc.Register(ctx, command)}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for result := range results {
		if result.err == nil {
			successes++
			continue
		}
		if errors.Is(result.err, domain.ErrUsernameTaken) {
			conflicts++
			continue
		}
		require.NoError(t, result.err)
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	require.Len(t, mailer.Entries(), 1)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM player_accounts`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestAccountRegisterConcurrentDuplicateEmailSendsOnlyStoredToken(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, _, _, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()
	email := uniq("same_email") + "@example.test"
	commands := []inbound.RegisterPlayerCommand{
		{Username: uniq("first_name"), Email: email, Password: accountTestPassword},
		{Username: uniq("second_name"), Email: email, Password: accountReplacementPassword},
	}
	start := make(chan struct{})
	results := make(chan error, len(commands))
	var workers sync.WaitGroup
	for _, command := range commands {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- uc.Register(ctx, command)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err, "email duplication is reported through the generic registration acknowledgement")
	}
	entries := mailer.Entries()
	require.Len(t, entries, 1)
	require.NoError(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, entries[0])))

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM player_accounts`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestAccountRegisterDuplicateEmailDoesNotReplacePendingCredentialsOrToken(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, _, _, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()
	email := uniq("pending") + "@example.test"
	registerTestAccount(t, uc, uniq("pending_name"), email)
	entries := mailer.Entries()
	require.Len(t, entries, 1)

	var originalPasswordHash string
	var originalTokenHash []byte
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT password_hash, verification_token_hash
		FROM player_accounts
		WHERE email_normalized = lower($1)`, email).Scan(&originalPasswordHash, &originalTokenHash))
	err := uc.Register(ctx, inbound.RegisterPlayerCommand{
		Username: uniq("replacement_name"),
		Email:    strings.ToUpper(email),
		Password: accountReplacementPassword,
	})
	require.NoError(t, err, "duplicate email must return the generic registration acknowledgement")
	require.Len(t, mailer.Entries(), 1, "duplicate email must not send a bogus replacement token")

	var passwordHash string
	var tokenHash []byte
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT password_hash, verification_token_hash
		FROM player_accounts
		WHERE email_normalized = lower($1)`, email).Scan(&passwordHash, &tokenHash))
	require.Equal(t, originalPasswordHash, passwordHash)
	require.Equal(t, originalTokenHash, tokenHash)
	require.NoError(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, entries[0])))
}

func TestAccountVerificationRejectsExpiredAndReplayedTokens(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, _, clock, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()

	registerTestAccount(t, uc, uniq("expired"), uniq("expired")+"@example.test")
	expiredMail := mailer.Entries()[0]
	clock.Advance(25 * time.Hour)
	require.ErrorIs(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, expiredMail)), domain.ErrVerificationTokenInvalid)

	registerTestAccount(t, uc, uniq("replay"), uniq("replay")+"@example.test")
	validMail := mailer.Entries()[1]
	token := verificationTokenFromMail(t, validMail)
	require.NoError(t, uc.VerifyEmail(ctx, token))
	require.ErrorIs(t, uc.VerifyEmail(ctx, token), domain.ErrVerificationTokenInvalid)
}

func TestAccountResendCooldownRotatesVerificationToken(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, _, clock, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()
	email := uniq("resend") + "@example.test"
	registerTestAccount(t, uc, uniq("resend_name"), email)
	initial := mailer.Entries()
	require.Len(t, initial, 1)

	require.NoError(t, uc.ResendVerification(ctx, strings.ToUpper(email)))
	require.Len(t, mailer.Entries(), 1, "resend cooldown should suppress immediate mail")
	clock.Advance(61 * time.Second)
	require.NoError(t, uc.ResendVerification(ctx, email))
	entries := mailer.Entries()
	require.Len(t, entries, 2)
	require.ErrorIs(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, initial[0])), domain.ErrVerificationTokenInvalid)
	require.NoError(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, entries[1])))
}

func TestAccountResendVerificationForLoginUsesStoredRecipient(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, _, clock, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()

	username := uniq("resend_name")
	emailForUsername := uniq("resend_user") + "@example.test"
	registerTestAccount(t, uc, username, emailForUsername)
	first := mailer.Entries()
	require.Len(t, first, 1)
	require.Equal(t, emailForUsername, first[0].recipient)
	clock.Advance(61 * time.Second)

	err := uc.ResendVerificationForLogin(ctx, inbound.LoginPlayerCommand{
		Login:    username,
		Password: "Wrong-test-password-284",
	})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	require.Len(t, mailer.Entries(), 1)

	err = uc.ResendVerificationForLogin(ctx, inbound.LoginPlayerCommand{
		Login:    username,
		Password: accountTestPassword,
	})
	require.NoError(t, err)
	entries := mailer.Entries()
	require.Len(t, entries, 2)
	require.Equal(t, emailForUsername, entries[1].recipient)
	var accountHasPlayer bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT player_id IS NOT NULL
		FROM player_accounts
		WHERE email_normalized = lower($1)`, emailForUsername).Scan(&accountHasPlayer))
	require.False(t, accountHasPlayer)
	require.ErrorIs(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, first[0])), domain.ErrVerificationTokenInvalid)
	require.NoError(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, entries[1])))

	usernameForEmail := uniq("resend_email")
	emailForEmail := "MiXeD-" + uniq("recipient") + "@example.test"
	registerTestAccount(t, uc, usernameForEmail, emailForEmail)
	entries = mailer.Entries()
	require.Len(t, entries, 3)
	require.Equal(t, emailForEmail, entries[2].recipient)
	clock.Advance(61 * time.Second)
	err = uc.ResendVerificationForLogin(ctx, inbound.LoginPlayerCommand{
		Login:    strings.ToUpper(emailForEmail),
		Password: accountTestPassword,
	})
	require.NoError(t, err)
	entries = mailer.Entries()
	require.Len(t, entries, 4)
	require.Equal(t, emailForEmail, entries[3].recipient)
	require.ErrorIs(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, entries[2])), domain.ErrVerificationTokenInvalid)
	require.NoError(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, entries[3])))
}

func TestAccountResendVerificationForLoginCooldownIsAtomic(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, _, clock, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()
	username := uniq("resend_race")
	email := uniq("resend_race") + "@example.test"
	registerTestAccount(t, uc, username, email)
	initial := mailer.Entries()
	require.Len(t, initial, 1)
	clock.Advance(61 * time.Second)

	commands := []inbound.LoginPlayerCommand{
		{Login: username, Password: accountTestPassword},
		{Login: email, Password: accountTestPassword},
	}
	start := make(chan struct{})
	results := make(chan error, len(commands))
	var workers sync.WaitGroup
	for _, command := range commands {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- uc.ResendVerificationForLogin(ctx, command)
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	limited := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrRateLimited):
			limited++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, limited)
	entries := mailer.Entries()
	require.Len(t, entries, 2)
	require.Equal(t, email, entries[1].recipient)
	require.ErrorIs(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, initial[0])), domain.ErrVerificationTokenInvalid)
	require.NoError(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, entries[1])))
}

func TestAccountRegistrationPreservesLegacyUsernameReservation(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, players, _, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()
	legacy, err := players.Create(ctx, uniq("LegacyNick"))
	require.NoError(t, err)

	err = uc.Register(ctx, inbound.RegisterPlayerCommand{
		Username: strings.ToUpper(legacy.Username),
		Email:    uniq("legacy") + "@example.test",
		Password: accountTestPassword,
	})
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
	require.Empty(t, mailer.Entries())

	var username string
	require.NoError(t, pool.QueryRow(ctx, `SELECT username FROM players WHERE id = $1`, legacy.ID).Scan(&username))
	require.Equal(t, legacy.Username, username)
}

func TestAccountLoginExposesVerificationOnlyForCorrectPassword(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, _, _, _ := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()
	email := uniq("pending_login") + "@example.test"
	registerTestAccount(t, uc, uniq("pending_login_name"), email)

	player, err := uc.Login(ctx, inbound.LoginPlayerCommand{
		Login:    email,
		Password: "Wrong-test-password-284",
	})
	require.Nil(t, player)
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)

	player, err = uc.Login(ctx, inbound.LoginPlayerCommand{Login: email, Password: accountTestPassword})
	require.Nil(t, player)
	require.ErrorIs(t, err, domain.ErrEmailUnverified)

	var accountHasPlayer, hasSession bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT player_id IS NOT NULL,
			EXISTS (
				SELECT 1
				FROM players
				WHERE players.id = player_accounts.player_id
					AND players.session_token IS NOT NULL
			)
		FROM player_accounts
		WHERE email_normalized = lower($1)`, email).Scan(&accountHasPlayer, &hasSession))
	require.False(t, accountHasPlayer)
	require.False(t, hasSession)
}

func TestAccountLoginRejectsSoftDeletedPlayerAndSession(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	uc, players, _, mailer := newAccountLifecycleFixture(t, pool)
	ctx := context.Background()
	email := uniq("deleted") + "@example.test"
	registerTestAccount(t, uc, uniq("deleted_name"), email)
	entry := mailer.Entries()[0]
	require.NoError(t, uc.VerifyEmail(ctx, verificationTokenFromMail(t, entry)))
	player, err := uc.Login(ctx, inbound.LoginPlayerCommand{Login: email, Password: accountTestPassword})
	require.NoError(t, err)
	require.NotNil(t, player.SessionToken)

	err = players.SoftDeletePlayer(ctx, player.ID, uniq("deleted_player"), time.Now().UTC())
	require.NoError(t, err)
	_, err = uc.Login(ctx, inbound.LoginPlayerCommand{Login: email, Password: accountTestPassword})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	_, err = players.GetBySessionToken(ctx, *player.SessionToken)
	require.ErrorIs(t, err, domain.ErrAccountDeleted)
}
