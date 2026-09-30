package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/google/uuid"
)

const (
	defaultVerificationTTL = 24 * time.Hour
	defaultResendCooldown  = time.Minute
	defaultSessionTTL      = 24 * time.Hour
	maxPasswordBytes       = 512
	activationTokenBytes   = 32
	dummyPasswordBytes     = 32
)

var playerUsernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,50}$`)

type Config struct {
	VerificationPageURL string
	VerificationTTL     time.Duration
	ResendCooldown      time.Duration
	SessionTTL          time.Duration
}

type UseCase struct {
	cfg       Config
	tx        TransactionManager
	accounts  Repository
	passwords PasswordHasher
	mailer    VerificationMailer
	clock     Clock
	dummyHash string
}

var _ inbound.PlayerAccountService = (*UseCase)(nil)

func NewUseCase(
	cfg Config,
	tx TransactionManager,
	accounts Repository,
	passwords PasswordHasher,
	mailer VerificationMailer,
	clock Clock,
) (*UseCase, error) {
	if tx == nil || accounts == nil || passwords == nil || clock == nil {
		return nil, domain.ErrInternal
	}
	if cfg.VerificationTTL <= 0 {
		cfg.VerificationTTL = defaultVerificationTTL
	}
	if cfg.ResendCooldown <= 0 {
		cfg.ResendCooldown = defaultResendCooldown
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultSessionTTL
	}
	dummyPassword := make([]byte, dummyPasswordBytes)
	if _, err := rand.Read(dummyPassword); err != nil {
		return nil, fmt.Errorf("create player account timing password: %w", err)
	}
	dummyHash, err := passwords.Hash(base64.RawURLEncoding.EncodeToString(dummyPassword))
	if err != nil {
		return nil, fmt.Errorf("create player account timing hash: %w", err)
	}
	return &UseCase{
		cfg:       cfg,
		tx:        tx,
		accounts:  accounts,
		passwords: passwords,
		mailer:    mailer,
		clock:     clock,
		dummyHash: dummyHash,
	}, nil
}

func (u *UseCase) Register(ctx context.Context, command inbound.RegisterPlayerCommand) error {
	if !u.canSendVerification() {
		return domain.ErrVerificationUnavailable
	}
	username, normalizedUsername, err := normalizeUsername(command.Username)
	if err != nil {
		return err
	}
	email, normalizedEmail, err := normalizeEmail(command.Email)
	if err != nil {
		return err
	}
	if err := validatePassword(command.Password); err != nil {
		return err
	}

	passwordHash, err := u.passwords.Hash(command.Password)
	if err != nil {
		return fmt.Errorf("hash player password: %w", err)
	}
	token, tokenHash, err := newVerificationToken()
	if err != nil {
		return fmt.Errorf("create player verification token: %w", err)
	}
	now := u.clock.Now().UTC()
	account := PendingAccount{
		Username:              username,
		UsernameNormalized:    normalizedUsername,
		Email:                 email,
		EmailNormalized:       normalizedEmail,
		PasswordHash:          passwordHash,
		VerificationTokenHash: tokenHash,
		VerificationExpiresAt: now.Add(u.cfg.VerificationTTL),
		VerificationSentAt:    now,
	}

	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		return u.accounts.CreatePendingAccount(txCtx, account)
	}); err != nil {
		if errors.Is(err, domain.ErrEmailTaken) {
			return nil
		}
		return err
	}
	if err := u.sendVerification(ctx, email, token); err != nil {
		return err
	}
	return nil
}

func (u *UseCase) Login(ctx context.Context, command inbound.LoginPlayerCommand) (*domain.Player, error) {
	login, err := normalizeLogin(command)
	if err != nil {
		return nil, err
	}
	credentials, err := u.findLoginCredentials(ctx, login, command.Password)
	if err != nil {
		return nil, err
	}
	if err := u.verifyLoginCredentials(command.Password, credentials); err != nil {
		return nil, err
	}
	return u.rotateLoginSession(ctx, *credentials.PlayerID)
}

func normalizeLogin(command inbound.LoginPlayerCommand) (string, error) {
	login := strings.ToLower(strings.TrimSpace(command.Login))
	if login == "" || len(login) > 254 || command.Password == "" || len(command.Password) > maxPasswordBytes ||
		!utf8.ValidString(command.Password) || strings.IndexByte(command.Password, 0) >= 0 {
		return "", domain.ErrValidation
	}
	return login, nil
}

func (u *UseCase) findLoginCredentials(ctx context.Context, login, password string) (*LoginCredentials, error) {
	credentials, err := u.accounts.FindLoginCredentials(ctx, login)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			_, verifyErr := u.passwords.Verify(password, u.dummyHash)
			if verifyErr != nil {
				return nil, fmt.Errorf("verify unavailable player account password: %w", verifyErr)
			}
			return nil, domain.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("find player login credentials: %w", err)
	}
	if credentials == nil {
		return nil, domain.ErrInternal
	}
	return credentials, nil
}

func (u *UseCase) verifyLoginCredentials(password string, credentials *LoginCredentials) error {
	validPassword, err := u.passwords.Verify(password, credentials.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify player password: %w", err)
	}
	if !validPassword || credentials.PlayerID == nil || credentials.EmailVerifiedAt == nil {
		return domain.ErrInvalidCredentials
	}
	return nil
}

func (u *UseCase) rotateLoginSession(ctx context.Context, playerID uuid.UUID) (*domain.Player, error) {
	token, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("create player session token: %w", err)
	}
	now := u.clock.Now().UTC()
	var player *domain.Player
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		updated, err := u.accounts.UpdateAccountPlayerSession(
			txCtx,
			playerID,
			token,
			now.Add(u.cfg.SessionTTL),
		)
		if err != nil {
			return err
		}
		player = updated
		return nil
	}); err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			return nil, domain.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("persist player login session: %w", err)
	}
	return player, nil
}

func (u *UseCase) VerifyEmail(ctx context.Context, token string) error {
	tokenHash, err := decodeVerificationToken(token)
	if err != nil {
		return domain.ErrVerificationTokenInvalid
	}
	return u.tx.Do(ctx, func(txCtx context.Context) error {
		player, err := u.accounts.VerifyPendingAccount(txCtx, tokenHash, u.clock.Now().UTC())
		if err != nil {
			return err
		}
		if player == nil || player.ID == uuid.Nil {
			return domain.ErrInternal
		}
		return nil
	})
}

func (u *UseCase) ResendVerification(ctx context.Context, rawEmail string) error {
	if !u.canSendVerification() {
		return domain.ErrVerificationUnavailable
	}
	_, normalizedEmail, err := normalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	token, tokenHash, err := newVerificationToken()
	if err != nil {
		return fmt.Errorf("create player verification token: %w", err)
	}
	now := u.clock.Now().UTC()
	var pending *PendingVerification
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		pending, err = u.accounts.ReplacePendingVerification(
			txCtx,
			normalizedEmail,
			now.Add(-u.cfg.ResendCooldown),
			tokenHash,
			now.Add(u.cfg.VerificationTTL),
			now,
		)
		return err
	}); err != nil {
		return err
	}
	if pending == nil {
		return nil
	}
	return u.sendVerification(ctx, pending.Email, token)
}

func (u *UseCase) canSendVerification() bool {
	if u == nil || u.mailer == nil || strings.TrimSpace(u.cfg.VerificationPageURL) == "" {
		return false
	}
	parsed, err := url.Parse(u.cfg.VerificationPageURL)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") &&
		parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func (u *UseCase) sendVerification(ctx context.Context, email, token string) error {
	if !u.canSendVerification() {
		return domain.ErrVerificationUnavailable
	}
	pageURL, err := url.Parse(u.cfg.VerificationPageURL)
	if err != nil {
		return domain.ErrVerificationUnavailable
	}
	pageURL.Fragment = "token=" + token
	if err := u.mailer.SendVerification(ctx, email, pageURL.String()); err != nil {
		return domain.WrapError(err, domain.ErrVerificationUnavailable)
	}
	return nil
}

func normalizeUsername(username string) (string, string, error) {
	if !playerUsernameRE.MatchString(username) {
		return "", "", domain.ErrUsernameInvalid
	}
	return username, strings.ToLower(username), nil
}

func normalizeEmail(raw string) (string, string, error) {
	email := strings.TrimSpace(raw)
	if email == "" || len(email) > 254 {
		return "", "", domain.ErrValidation
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", "", domain.ErrValidation
	}
	return email, strings.ToLower(email), nil
}

func validatePassword(password string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 15 ||
		utf8.RuneCountInString(password) > 128 || len(password) > maxPasswordBytes ||
		strings.IndexByte(password, 0) >= 0 {
		return domain.ErrValidation
	}
	return nil
}

func newVerificationToken() (string, []byte, error) {
	value := make([]byte, activationTokenBytes)
	if _, err := rand.Read(value); err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(value)
	return base64.RawURLEncoding.EncodeToString(value), digest[:], nil
}

func decodeVerificationToken(token string) ([]byte, error) {
	if len(token) != 43 {
		return nil, domain.ErrVerificationTokenInvalid
	}
	value, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(value) != activationTokenBytes {
		return nil, domain.ErrVerificationTokenInvalid
	}
	digest := sha256.Sum256(value)
	return digest[:], nil
}
