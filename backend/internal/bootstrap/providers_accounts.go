package bootstrap

import (
	"strings"
	"time"

	"github.com/google/wire"

	"github.com/TakuyaYagam1/task-per-minute/config"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	mailadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/mail"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/account"
)

var PlayerAccountsSet = wire.NewSet(
	authadapter.NewPasswordHasher,
	wire.Bind(new(account.PasswordHasher), new(*authadapter.PasswordHasher)),
	provideVerificationMailer,
	wire.Bind(new(account.VerificationMailer), new(*mailadapter.Sender)),
	wire.Bind(new(account.AccountSettingsMailer), new(*mailadapter.Sender)),
	providePlayerAccounts,
	wire.Bind(new(inbound.PlayerAccountService), new(*account.UseCase)),
	provideAccountSettings,
	wire.Bind(new(inbound.AccountSettingsService), new(*account.AccountSettingsUseCase)),
)

func provideVerificationMailer(cfg *config.Config) (*mailadapter.Sender, error) {
	return mailadapter.New(mailadapter.Config{
		Provider:     cfg.Email.Provider,
		From:         cfg.Email.From,
		ResendAPIKey: cfg.Resend.APIKey,
		SMTP: mailadapter.SMTPConfig{
			Host:     cfg.SMTP.Host,
			Port:     cfg.SMTP.Port,
			Username: cfg.SMTP.Username,
			Password: cfg.SMTP.Password,
			TLSMode:  cfg.SMTP.TLSMode,
		},
		Timeout: cfg.Email.Timeout,
	})
}

func providePlayerAccounts(
	cfg *config.Config,
	tx account.TransactionManager,
	repository account.Repository,
	passwords account.PasswordHasher,
	mailer account.VerificationMailer,
	clock account.Clock,
) (*account.UseCase, error) {
	verificationURL := ""
	if cfg.Email.Provider != "disabled" && cfg.AppPublicURL != "" {
		verificationURL = strings.TrimRight(cfg.AppPublicURL, "/") + "/verify-email"
	}
	return account.NewUseCase(account.Config{
		VerificationPageURL: verificationURL,
		VerificationTTL:     24 * time.Hour,
		ResendCooldown:      time.Minute,
		SessionTTL:          cfg.Player.SessionTTL,
	}, tx, repository, passwords, mailer, clock)
}

func provideAccountSettings(
	cfg *config.Config,
	tx account.TransactionManager,
	repository account.SettingsRepository,
	passwords account.PasswordHasher,
	mailer account.AccountSettingsMailer,
	leaderboard playerusecase.LeaderboardInvalidator,
	clock account.Clock,
) (*account.AccountSettingsUseCase, error) {
	return account.NewAccountSettingsUseCase(account.SettingsConfig{
		SessionTTL: cfg.Player.SessionTTL,
	}, tx, repository, passwords, mailer, leaderboard, clock)
}
