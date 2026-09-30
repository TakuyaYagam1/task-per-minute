package bootstrap

import (
	"strings"
	"time"

	"github.com/google/wire"

	"github.com/TakuyaYagam1/task-per-minute/config"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	mailadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/mail"
	accountrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/account"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/account"
)

var PlayerAccountsSet = wire.NewSet(
	accountrepo.NewAccountPostgres,
	wire.Bind(new(account.Repository), new(*accountrepo.AccountPostgres)),
	authadapter.NewPasswordHasher,
	wire.Bind(new(account.PasswordHasher), new(*authadapter.PasswordHasher)),
	provideVerificationMailer,
	wire.Bind(new(account.VerificationMailer), new(*mailadapter.Sender)),
	providePlayerAccounts,
	wire.Bind(new(inbound.PlayerAccountService), new(*account.UseCase)),
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
