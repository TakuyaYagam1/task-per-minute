package account

import (
	"context"
	"crypto/sha256"
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
		Email:    "Alice@example.test",
		Password: "a long passphrase with enough length",
	})
	require.NoError(t, err)
	require.Len(t, mailer.sent, 1)
	require.Equal(t, "Alice@example.test", mailer.sent[0].recipient)
	require.NotNil(t, repo.pending)
	require.Equal(t, "Alice", repo.pending.Username)
	require.Equal(t, "alice", repo.pending.UsernameNormalized)
	require.Equal(t, "alice@example.test", repo.pending.EmailNormalized)
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
		Password: "a long passphrase with enough length",
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.createCalls)
	require.Empty(t, mailer.sent)
}

func TestRegisterValidatesUnicodePasswordLengthBeforePersistence(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "below minimum", password: strings.Repeat("a", 14), wantErr: true},
		{name: "unicode maximum", password: strings.Repeat("🧩", 128)},
		{name: "above maximum", password: strings.Repeat("🧩", 129), wantErr: true},
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

func TestLoginRejectsUnverifiedAndMissingAccountsWithSameCredentialError(t *testing.T) {
	now := time.Now().UTC()
	verified := now
	playerID := uuid.New()
	repo := &accountRepositoryFake{
		credentials: &LoginCredentials{
			Username:        "alice",
			PasswordHash:    "test-password-hash",
			PlayerID:        &playerID,
			EmailVerifiedAt: nil,
		},
	}
	service := newTestAccountUseCase(t, repo, &verificationMailerFake{}, now)

	_, err := service.Login(context.Background(), inbound.LoginPlayerCommand{
		Login:    " Alice ",
		Password: "correct passphrase",
	})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	require.Equal(t, "alice", repo.lastLogin)
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
	pending       *PendingAccount
	createErr     error
	createCalls   int
	credentials   *LoginCredentials
	findErr       error
	lastLogin     string
	updatedPlayer *domain.Player
	updatedExpiry *time.Time
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

func (*accountRepositoryFake) ReplacePendingVerification(context.Context, string, time.Time, []byte, time.Time, time.Time) (*PendingVerification, error) {
	return nil, nil
}

func (*accountRepositoryFake) VerifyPendingAccount(context.Context, []byte, time.Time) (*domain.Player, error) {
	return nil, domain.ErrVerificationTokenInvalid
}

func (r *accountRepositoryFake) UpdateAccountPlayerSession(_ context.Context, _ uuid.UUID, token uuid.UUID, expiresAt time.Time) (*domain.Player, error) {
	r.updatedExpiry = &expiresAt
	player := &domain.Player{ID: uuid.New(), Username: "alice", SessionToken: &token, SessionExpiresAt: &expiresAt}
	r.updatedPlayer = player
	return player, nil
}

func newTestAccountUseCase(t *testing.T, repo Repository, mailer VerificationMailer, now time.Time) *UseCase {
	t.Helper()
	service, err := NewUseCase(Config{
		VerificationPageURL: "https://app.example.test/verify-email",
	}, inlineTransaction{}, repo, &passwordHasherFake{}, mailer, fixedAccountClock(now))
	require.NoError(t, err)
	return service
}
