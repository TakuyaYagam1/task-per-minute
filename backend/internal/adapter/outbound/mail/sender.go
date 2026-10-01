package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

const (
	ProviderDisabled = "disabled"
	ProviderResend   = "resend"
	ProviderSMTP     = "smtp"

	SMTPModeStartTLS = "starttls"
	SMTPModeTLS      = "tls"

	resendAPIURL = "https://api.resend.com/emails"
)

var ErrUnavailable = errors.New("mail delivery is unavailable")

type Config struct {
	Provider     string
	From         string
	ResendAPIKey string
	SMTP         SMTPConfig
	Timeout      time.Duration
}

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	TLSMode  string
}

type Sender struct {
	provider     string
	from         string
	resendAPIKey string
	timeout      time.Duration
	httpClient   *http.Client
	resendURL    string
	smtp         *smtpSender
}

type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	HTML    string   `json:"html"`
}

func New(cfg Config) (*Sender, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = ProviderDisabled
	}
	if provider == ProviderDisabled {
		return &Sender{provider: provider}, nil
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("mail timeout must be positive")
	}
	from, err := normalizeAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mail sender address is invalid")
	}
	sender := &Sender{provider: provider, from: from, timeout: cfg.Timeout}
	switch provider {
	case ProviderResend:
		apiKey := strings.TrimSpace(cfg.ResendAPIKey)
		if apiKey == "" {
			return nil, fmt.Errorf("resend API key is required")
		}
		sender.resendAPIKey = apiKey
		sender.resendURL = resendAPIURL
		sender.httpClient = &http.Client{
			Transport: http.DefaultTransport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	case ProviderSMTP:
		smtp, err := newSMTPSender(cfg.SMTP, cfg.Timeout, nil, nil)
		if err != nil {
			return nil, err
		}
		sender.smtp = smtp
	default:
		return nil, fmt.Errorf("unsupported mail provider")
	}
	return sender, nil
}

func (s *Sender) SendVerification(ctx context.Context, recipient, verificationURL string) error {
	if s == nil || s.provider == ProviderDisabled {
		return ErrUnavailable
	}
	if ctx == nil {
		return fmt.Errorf("mail context must not be nil")
	}
	to, err := normalizeAddress(recipient)
	if err != nil {
		return fmt.Errorf("mail recipient address is invalid")
	}
	link, err := normalizeVerificationURL(verificationURL)
	if err != nil {
		return fmt.Errorf("mail verification URL is invalid")
	}
	message, err := verificationMessage(s.from, to, link)
	if err != nil {
		return err
	}
	return s.send(ctx, message)
}

func (s *Sender) SendEmailChangeCode(ctx context.Context, recipient, code string) error {
	if s == nil || s.provider == ProviderDisabled {
		return ErrUnavailable
	}
	if ctx == nil {
		return fmt.Errorf("mail context must not be nil")
	}
	to, err := normalizeAddress(recipient)
	if err != nil {
		return fmt.Errorf("mail recipient address is invalid")
	}
	if !validEmailChangeCode(code) {
		return fmt.Errorf("email change code is invalid")
	}
	message, err := emailChangeCodeMessage(s.from, to, code)
	if err != nil {
		return err
	}
	return s.send(ctx, message)
}

func (s *Sender) SendEmailChangedNotice(ctx context.Context, previousEmail, newEmail string) error {
	if s == nil || s.provider == ProviderDisabled {
		return ErrUnavailable
	}
	if ctx == nil {
		return fmt.Errorf("mail context must not be nil")
	}
	previous, err := normalizeAddress(previousEmail)
	if err != nil {
		return fmt.Errorf("previous email address is invalid")
	}
	next, err := normalizeAddress(newEmail)
	if err != nil {
		return fmt.Errorf("new email address is invalid")
	}
	message, err := emailChangedNoticeMessage(s.from, previous, next)
	if err != nil {
		return err
	}
	return s.send(ctx, message)
}

func (s *Sender) send(ctx context.Context, message verificationEmail) error {
	sendCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	switch s.provider {
	case ProviderResend:
		return s.sendResend(sendCtx, message)
	case ProviderSMTP:
		return s.smtp.send(sendCtx, message)
	default:
		return ErrUnavailable
	}
}

func (s *Sender) sendResend(ctx context.Context, message verificationEmail) error {
	body, err := json.Marshal(resendRequest{
		From:    message.from,
		To:      []string{message.to},
		Subject: message.subject,
		Text:    message.text,
		HTML:    message.html,
	})
	if err != nil {
		return fmt.Errorf("mail: encode Resend message: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.resendURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mail: create Resend request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.resendAPIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("mail: send Resend request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.CopyN(io.Discard, response.Body, 64*1024)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("mail: Resend returned HTTP %d", response.StatusCode)
	}
	return nil
}

type verificationEmail struct {
	from    string
	to      string
	subject string
	text    string
	html    string
}

func verificationMessage(from, to, link string) (verificationEmail, error) {
	textBody, htmlBody, err := renderMailTemplates("verification", mailTemplateData{VerificationURL: link})
	if err != nil {
		return verificationEmail{}, err
	}
	return verificationEmail{
		from:    from,
		to:      to,
		subject: "Подтвердите регистрацию в Task Per Minute",
		text:    textBody,
		html:    htmlBody,
	}, nil
}

func emailChangeCodeMessage(from, to, code string) (verificationEmail, error) {
	textBody, htmlBody, err := renderMailTemplates("email_change_code", mailTemplateData{Code: code})
	if err != nil {
		return verificationEmail{}, err
	}
	return verificationEmail{
		from:    from,
		to:      to,
		subject: "Код подтверждения нового адреса Task Per Minute",
		text:    textBody,
		html:    htmlBody,
	}, nil
}

func emailChangedNoticeMessage(from, previousEmail, newEmail string) (verificationEmail, error) {
	textBody, htmlBody, err := renderMailTemplates("email_changed_notice", mailTemplateData{NewEmail: newEmail})
	if err != nil {
		return verificationEmail{}, err
	}
	return verificationEmail{
		from:    from,
		to:      previousEmail,
		subject: "Адрес электронной почты Task Per Minute изменен",
		text:    textBody,
		html:    htmlBody,
	}, nil
}

func validEmailChangeCode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func normalizeAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("invalid address")
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed == nil || parsed.Name != "" || parsed.Address != value {
		return "", fmt.Errorf("invalid address")
	}
	return parsed.Address, nil
}

func normalizeVerificationURL(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("invalid verification URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || !validVerificationURLShape(parsed) {
		return "", fmt.Errorf("invalid verification URL")
	}
	if err := validateVerificationURLScheme(parsed); err != nil {
		return "", err
	}
	if err := validateVerificationURLFragment(parsed.Fragment); err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func validVerificationURLShape(parsed *url.URL) bool {
	return parsed != nil && parsed.IsAbs() && parsed.Host != "" && parsed.User == nil &&
		parsed.RawQuery == "" && !parsed.ForceQuery && parsed.RawFragment == ""
}

func validateVerificationURLScheme(parsed *url.URL) error {
	if parsed.Scheme == "https" || (parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return nil
	}
	return fmt.Errorf("verification URL must use HTTPS except for loopback development URLs")
}

func validateVerificationURLFragment(fragment string) error {
	token, hasToken := strings.CutPrefix(fragment, "token=")
	if !hasToken || token == "" || len(token) > 256 {
		return fmt.Errorf("invalid verification URL fragment")
	}
	for index := range len(token) {
		if !isBase64URLByte(token[index]) {
			return fmt.Errorf("invalid verification URL fragment")
		}
	}
	return nil
}

func isBase64URLByte(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9') || value == '-' || value == '_'
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
