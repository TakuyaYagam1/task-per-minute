package account

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestRegisterSendsFragmentTokenAndPersistsOnlyDigest(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	repo := &accountRepositoryFake{}
	mailer := &verificationMailerFake{}
	service := newTestAccountUseCase(t, repo, mailer, now)

	err := service.Register(context.Background(), inbound.RegisterPlayerCommand{
		Username: "Alice",
		Email:    " Alice+tag@Sub.Example.test ",
		Password: "abcdefghijklA1!",
	})
	require.NoError(t, err)
	require.Len(t, mailer.sent, 1)
	require.Equal(t, "Alice+tag@Sub.Example.test", mailer.sent[0].recipient)
	require.NotNil(t, repo.pending)
	require.Equal(t, "Alice", repo.pending.Username)
	require.Equal(t, "alice", repo.pending.UsernameNormalized)
	require.Equal(t, "alice+tag@sub.example.test", repo.pending.EmailNormalized)
	require.Equal(t, "test-password-hash", repo.pending.PasswordHash)
	require.WithinDuration(t, now.Add(24*time.Hour), repo.pending.VerificationExpiresAt, time.Second)
	require.Len(t, repo.pending.VerificationTokenHash, sha256.Size)

	link, err := url.Parse(mailer.sent[0].verificationURL)
	require.NoError(t, err)
	require.Equal(t, "https", link.Scheme)
	require.Empty(t, link.RawQuery)
	require.True(t, strings.HasPrefix(link.Fragment, "token="))
	token := strings.TrimPrefix(link.Fragment, "token=")
	digest, err := decodeVerificationToken(token)
	require.NoError(t, err)
	require.Equal(t, repo.pending.VerificationTokenHash, digest)
	require.NotContains(t, mailer.sent[0].verificationURL, string(repo.pending.VerificationTokenHash))
}

func TestRegisterExistingEmailReturnsGenericAckWithoutSendingAnotherMail(t *testing.T) {
	repo := &accountRepositoryFake{createErr: domain.ErrEmailTaken}
	mailer := &verificationMailerFake{}
	service := newTestAccountUseCase(t, repo, mailer, time.Now().UTC())

	err := service.Register(context.Background(), inbound.RegisterPlayerCommand{
		Username: "AnotherName",
		Email:    "already-used@example.test",
		Password: "abcdefghijklA1!",
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.createCalls)
	require.Empty(t, mailer.sent)
}

func TestRegisterEnforcesPasswordLengthAndCharacterClassesBeforePersistence(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "below minimum", password: strings.Repeat("a", 6) + "A1!", wantErr: true},
		{name: "missing lowercase", password: strings.Repeat("A", 13) + "1!", wantErr: true},
		{name: "missing uppercase", password: strings.Repeat("a", 13) + "1!", wantErr: true},
		{name: "missing decimal digit", password: strings.Repeat("a", 13) + "A!", wantErr: true},
		{name: "missing punctuation or symbol", password: strings.Repeat("a", 13) + "A1", wantErr: true},
		{name: "space does not count as punctuation or symbol", password: strings.Repeat("a", 12) + "A1 ", wantErr: true},
		{name: "valid minimum with unicode classes", password: strings.Repeat("ā", 7) + "Ω٧🧩"},
		{name: "valid maximum", password: strings.Repeat("a", 125) + "A1!"},
		{name: "above maximum", password: strings.Repeat("a", 126) + "A1!", wantErr: true},
		{name: "invalid utf8", password: string([]byte{0xff}) + strings.Repeat("a", 12) + "A1!", wantErr: true},
		{name: "nul byte", password: strings.Repeat("a", 12) + "A1\x00", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &accountRepositoryFake{}
			service := newTestAccountUseCase(t, repo, &verificationMailerFake{}, time.Now().UTC())
			err := service.Register(context.Background(), inbound.RegisterPlayerCommand{
				Username: "UnicodeUser",
				Email:    "unicode@example.test",
				Password: test.password,
			})
			if test.wantErr {
				require.ErrorIs(t, err, domain.ErrValidation)
				require.Zero(t, repo.createCalls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, repo.createCalls)
		})
	}
}

func TestEmailChangeCooldownAllowsFifthSendAtCooldownBoundary(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	sendWindow := now.Add(-10 * time.Minute)
	lastSent := now.Add(-30 * time.Second)
	record := &AccountSettingsRecord{
		EmailChangeSendWindowStartedAt: &sendWindow,
		EmailChangeSendCount:           4,
		EmailChangeLastSentAt:          &lastSent,
	}

	_, _, _, _, err := emailChangeSendState(record, now)
	require.ErrorIs(t, err, domain.ErrEmailChangeRateLimited)
	var retryAfter interface{ RetryAfter() time.Duration }
	require.ErrorAs(t, err, &retryAfter)
	require.Equal(t, 30*time.Second, retryAfter.RetryAfter())

	window, count, _, _, err := emailChangeSendState(record, lastSent.Add(emailChangeCooldown))
	require.NoError(t, err)
	require.Equal(t, sendWindow, window)
	require.Equal(t, maxEmailChangeSends, count)
}

func TestRegisterRejectsMalformedMailboxAndDomains(t *testing.T) {
	tests := []struct {
		name  string
		email string
	}{
		{name: "domain without a dot", email: "name@gmail"},
		{name: "localhost", email: "name@localhost"},
		{name: "domain literal", email: "name@[127.0.0.1]"},
		{name: "empty label", email: "name@example..test"},
		{name: "leading hyphen", email: "name@-example.test"},
		{name: "trailing hyphen", email: "name@example-.test"},
		{name: "label too long", email: "name@" + strings.Repeat("a", 64) + ".test"},
		{name: "numeric top-level label", email: "name@example.123"},
		{name: "display name", email: "Name <name@example.test>"},
		{name: "quoted local part", email: `"name"@example.test`},
		{name: "consecutive local dots", email: "first..last@example.test"},
		{name: "local part too long", email: strings.Repeat("a", 65) + "@example.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &accountRepositoryFake{}
			mailer := &verificationMailerFake{}
			service := newTestAccountUseCase(t, repo, mailer, time.Now().UTC())
			err := service.Register(context.Background(), inbound.RegisterPlayerCommand{
				Username: "EmailUser",
				Email:    test.email,
				Password: "abcdefghijklA1!",
			})
			require.ErrorIs(t, err, domain.ErrValidation)
			require.Zero(t, repo.createCalls)
			require.Empty(t, mailer.sent)
		})
	}
}

func TestNormalizeEmailAcceptsPunycodeTopLevelDomain(t *testing.T) {
	email, normalized, err := normalizeEmail("User+tag@accounts.xn--p1ai")

	require.NoError(t, err)
	require.Equal(t, "User+tag@accounts.xn--p1ai", email)
	require.Equal(t, "user+tag@accounts.xn--p1ai", normalized)
}

func TestResendVerificationForLoginUsesStoredAddressAndRotatesOnlyAfterCooldown(t *testing.T) {
	for _, test := range []struct {
		name  string
		login string
	}{
		{name: "username", login: "ALICE"},
		{name: "email", login: "ALICE+TAG@EXAMPLE.TEST"},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
			clock := &mutableAccountClock{now: now}
			storedEmail := "Alice+tag@Example.test"
			repo := &accountRepositoryFake{
				credentials: &LoginCredentials{
					Username:     "alice",
					Email:        storedEmail,
					PasswordHash: "test-password-hash",
				},
				pendingVerification:    &PendingVerification{Email: storedEmail},
				lastVerificationSentAt: now.Add(-defaultResendCooldown),
			}
			mailer := &verificationMailerFake{}
			service := newTestAccountUseCaseWithClock(t, repo, mailer, clock)
			command := inbound.LoginPlayerCommand{Login: test.login, Password: "correct passphrase"}

			require.NoError(t, service.ResendVerificationForLogin(context.Background(), command))
			require.Equal(t, "alice+tag@example.test", repo.lastReplacementEmail)
			require.Equal(t, storedEmail, mailer.sent[0].recipient)
			require.Nil(t, repo.updatedPlayer)
			require.Equal(t, strings.ToLower(test.login), repo.lastLogin)
			firstDigest := sentVerificationDigest(t, mailer.sent[0].verificationURL)
			require.Equal(t, repo.replacementTokenHashes[0], firstDigest)

			require.ErrorIs(t, service.ResendVerificationForLogin(context.Background(), command), domain.ErrRateLimited)
			require.Len(t, mailer.sent, 1)

			clock.now = now.Add(defaultResendCooldown)
			require.NoError(t, service.ResendVerificationForLogin(context.Background(), command))
			require.Len(t, mailer.sent, 2)
			require.Equal(t, storedEmail, mailer.sent[1].recipient)
			secondDigest := sentVerificationDigest(t, mailer.sent[1].verificationURL)
			require.Equal(t, repo.replacementTokenHashes[1], secondDigest)
			require.NotEqual(t, firstDigest, secondDigest)
		})
	}
}

func TestResendVerificationForLoginRejectsWrongUnknownAndVerifiedAccounts(t *testing.T) {
	verifiedAt := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	tests := []struct {
		name        string
		credentials *LoginCredentials
		findErr     error
		password    string
		wantErr     error
	}{
		{
			name: "wrong password",
			credentials: &LoginCredentials{
				Username: "alice", Email: "alice@example.test", PasswordHash: "test-password-hash",
			},
			password: "incorrect passphrase",
			wantErr:  domain.ErrInvalidCredentials,
		},
		{
			name:     "unknown login",
			findErr:  domain.ErrPlayerNotFound,
			password: "correct passphrase",
			wantErr:  domain.ErrInvalidCredentials,
		},
		{
			name: "already verified",
			credentials: &LoginCredentials{
				Username: "alice", Email: "alice@example.test", PasswordHash: "test-password-hash",
				PlayerID: &playerID, EmailVerifiedAt: &verifiedAt,
			},
			password: "correct passphrase",
			wantErr:  domain.ErrEmailAlreadyVerified,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &accountRepositoryFake{credentials: test.credentials, findErr: test.findErr}
			mailer := &verificationMailerFake{}
			service := newTestAccountUseCase(t, repo, mailer, verifiedAt)

			err := service.ResendVerificationForLogin(context.Background(), inbound.LoginPlayerCommand{
				Login: "alice", Password: test.password,
			})

			require.ErrorIs(t, err, test.wantErr)
			require.Zero(t, repo.replaceCalls)
			require.Empty(t, mailer.sent)
			require.Nil(t, repo.updatedPlayer)
		})
	}
}

func TestResendVerificationForLoginMapsMailFailureAndPublicResendKeepsGenericAck(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	repo := &accountRepositoryFake{
		credentials: &LoginCredentials{
			Username: "alice", Email: "alice@example.test", PasswordHash: "test-password-hash",
		},
		pendingVerification:    &PendingVerification{Email: "alice@example.test"},
		lastVerificationSentAt: now.Add(-defaultResendCooldown),
	}
	mailer := &verificationMailerFake{err: errors.New("mail transport unavailable")}
	service := newTestAccountUseCase(t, repo, mailer, now)

	err := service.ResendVerificationForLogin(context.Background(), inbound.LoginPlayerCommand{
		Login: "alice", Password: "correct passphrase",
	})
	require.ErrorIs(t, err, domain.ErrVerificationUnavailable)
	require.Len(t, mailer.sent, 1)
	require.Nil(t, repo.updatedPlayer)

	for _, test := range []struct {
		name string
		repo *accountRepositoryFake
	}{
		{name: "unknown email", repo: &accountRepositoryFake{}},
		{
			name: "cooldown",
			repo: &accountRepositoryFake{
				pendingVerification:    &PendingVerification{Email: "alice@example.test"},
				lastVerificationSentAt: now,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			publicMailer := &verificationMailerFake{}
			publicService := newTestAccountUseCase(t, test.repo, publicMailer, now)
			require.NoError(t, publicService.ResendVerification(context.Background(), "unknown@example.test"))
			require.Equal(t, 1, test.repo.replaceCalls)
			require.Empty(t, publicMailer.sent)
		})
	}
}

func TestLoginChecksPasswordBeforePendingAccountState(t *testing.T) {
	now := time.Now().UTC()
	verified := now
	playerID := uuid.New()
	repo := &accountRepositoryFake{
		credentials: &LoginCredentials{
			Username:        "alice",
			PasswordHash:    "test-password-hash",
			PlayerID:        nil,
			EmailVerifiedAt: nil,
		},
	}
	service := newTestAccountUseCase(t, repo, &verificationMailerFake{}, now)

	_, err := service.Login(context.Background(), inbound.LoginPlayerCommand{
		Login:    " Alice ",
		Password: "correct passphrase",
	})
	require.ErrorIs(t, err, domain.ErrEmailUnverified)
	require.Equal(t, "alice", repo.lastLogin)
	require.Nil(t, repo.updatedPlayer)

	_, err = service.Login(context.Background(), inbound.LoginPlayerCommand{
		Login:    " Alice ",
		Password: "incorrect passphrase",
	})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	require.Nil(t, repo.updatedPlayer)

	repo.findErr = domain.ErrPlayerNotFound
	repo.credentials = nil
	_, err = service.Login(context.Background(), inbound.LoginPlayerCommand{
		Login:    "missing@example.test",
		Password: "correct passphrase",
	})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	require.Contains(t, service.passwords.(*passwordHasherFake).verifiedHashes, "test-password-hash")

	repo.findErr = nil
	repo.credentials = &LoginCredentials{
		Username:        "alice",
		PasswordHash:    "test-password-hash",
		PlayerID:        &playerID,
		EmailVerifiedAt: &verified,
	}
	player, err := service.Login(context.Background(), inbound.LoginPlayerCommand{
		Login:    "ALICE@EXAMPLE.TEST",
		Password: "correct passphrase",
	})
	require.NoError(t, err)
	require.NotNil(t, player.SessionToken)
	require.NotEqual(t, uuid.Nil, *player.SessionToken)
	require.NotNil(t, repo.updatedPlayer)
	require.NotNil(t, repo.updatedExpiry)
}

type inlineTransaction struct{}

func (inlineTransaction) Do(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type fixedAccountClock time.Time

func (c fixedAccountClock) Now() time.Time { return time.Time(c) }

type mutableAccountClock struct {
	now time.Time
}

func (c *mutableAccountClock) Now() time.Time { return c.now }

type passwordHasherFake struct {
	verifiedHashes []string
}

func (*passwordHasherFake) Hash(string) (string, error) { return "test-password-hash", nil }

func (h *passwordHasherFake) Verify(password, hash string) (bool, error) {
	h.verifiedHashes = append(h.verifiedHashes, hash)
	return password == "correct passphrase" && hash == "test-password-hash", nil
}

type sentVerification struct {
	recipient       string
	verificationURL string
}

type verificationMailerFake struct {
	sent []sentVerification
	err  error
}

func (m *verificationMailerFake) SendVerification(_ context.Context, recipient, verificationURL string) error {
	m.sent = append(m.sent, sentVerification{recipient: recipient, verificationURL: verificationURL})
	return m.err
}

type accountRepositoryFake struct {
	pending                *PendingAccount
	createErr              error
	createCalls            int
	credentials            *LoginCredentials
	findErr                error
	lastLogin              string
	updatedPlayer          *domain.Player
	updatedExpiry          *time.Time
	pendingVerification    *PendingVerification
	replaceErr             error
	replaceCalls           int
	lastReplacementEmail   string
	lastVerificationSentAt time.Time
	replacementTokenHashes [][]byte
}

func (r *accountRepositoryFake) CreatePendingAccount(_ context.Context, value PendingAccount) error {
	r.createCalls++
	if r.createErr != nil {
		return r.createErr
	}
	pending := value
	pending.VerificationTokenHash = append([]byte(nil), value.VerificationTokenHash...)
	r.pending = &pending
	return nil
}

func (r *accountRepositoryFake) FindLoginCredentials(_ context.Context, login string) (*LoginCredentials, error) {
	r.lastLogin = login
	return r.credentials, r.findErr
}

func (r *accountRepositoryFake) ReplacePendingVerification(_ context.Context, email string, eligibleBefore time.Time, tokenHash []byte, _ time.Time, sentAt time.Time) (*PendingVerification, error) {
	r.replaceCalls++
	r.lastReplacementEmail = email
	if r.replaceErr != nil {
		return nil, r.replaceErr
	}
	if r.pendingVerification == nil || r.lastVerificationSentAt.After(eligibleBefore) {
		return nil, nil
	}
	r.lastVerificationSentAt = sentAt
	r.replacementTokenHashes = append(r.replacementTokenHashes, append([]byte(nil), tokenHash...))
	pending := *r.pendingVerification
	return &pending, nil
}

func (*accountRepositoryFake) VerifyPendingAccount(context.Context, []byte, time.Time) (*domain.Player, error) {
	return nil, domain.ErrVerificationTokenInvalid
}

func (r *accountRepositoryFake) UpdateAccountPlayerSession(_ context.Context, _ uuid.UUID, _, _ string, token uuid.UUID, expiresAt time.Time) (*domain.Player, error) {
	r.updatedExpiry = &expiresAt
	player := &domain.Player{ID: uuid.New(), Username: "alice", SessionToken: &token, SessionExpiresAt: &expiresAt}
	r.updatedPlayer = player
	return player, nil
}

func newTestAccountUseCase(t *testing.T, repo Repository, mailer VerificationMailer, now time.Time) *UseCase {
	t.Helper()
	return newTestAccountUseCaseWithClock(t, repo, mailer, fixedAccountClock(now))
}

func newTestAccountUseCaseWithClock(t *testing.T, repo Repository, mailer VerificationMailer, clock Clock) *UseCase {
	t.Helper()
	service, err := NewUseCase(Config{
		VerificationPageURL: "https://app.example.test/verify-email",
	}, inlineTransaction{}, repo, &passwordHasherFake{}, mailer, clock)
	require.NoError(t, err)
	return service
}

func sentVerificationDigest(t *testing.T, verificationURL string) []byte {
	t.Helper()
	link, err := url.Parse(verificationURL)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(link.Fragment, "token="))
	digest, err := decodeVerificationToken(strings.TrimPrefix(link.Fragment, "token="))
	require.NoError(t, err)
	return digest
}
