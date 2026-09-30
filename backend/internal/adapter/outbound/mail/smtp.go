package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type smtpSender struct {
	config      SMTPConfig
	dialContext func(context.Context, string, string) (net.Conn, error)
	rootCAs     *x509.CertPool
}

func newSMTPSender(
	cfg SMTPConfig,
	timeout time.Duration,
	dialContext func(context.Context, string, string) (net.Conn, error),
	rootCAs *x509.CertPool,
) (*smtpSender, error) {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.TLSMode = strings.ToLower(strings.TrimSpace(cfg.TLSMode))
	if cfg.Host == "" || strings.ContainsAny(cfg.Host, "/?#@ \t\r\n") || strings.Contains(cfg.Host, "://") {
		return nil, fmt.Errorf("SMTP host is invalid")
	}
	if net.ParseIP(cfg.Host) == nil {
		parsed, err := urlParseHost(cfg.Host)
		if err != nil || parsed != cfg.Host {
			return nil, fmt.Errorf("SMTP host is invalid")
		}
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("SMTP port must be between 1 and 65535")
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, fmt.Errorf("SMTP username and password must be set together")
	}
	if cfg.TLSMode != SMTPModeStartTLS && cfg.TLSMode != SMTPModeTLS {
		return nil, fmt.Errorf("SMTP TLS mode must be starttls or tls")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("SMTP timeout must be positive")
	}
	if dialContext == nil {
		dialer := &net.Dialer{Timeout: timeout}
		dialContext = dialer.DialContext
	}
	return &smtpSender{
		config:      cfg,
		dialContext: dialContext,
		rootCAs:     rootCAs,
	}, nil
}

func (s *smtpSender) send(ctx context.Context, message verificationEmail) error {
	address := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	rawConn, err := s.dialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("mail: SMTP connection failed")
	}
	stopClose := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stopClose()
	defer func() { _ = rawConn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := rawConn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("mail: SMTP connection setup failed")
		}
	}

	conn, err := s.implicitTLS(ctx, rawConn)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, s.config.Host)
	if err != nil {
		return fmt.Errorf("mail: SMTP greeting failed")
	}
	defer func() { _ = client.Close() }()
	if err := s.startTLS(client); err != nil {
		return err
	}
	if err := s.authenticate(client); err != nil {
		return err
	}
	return sendSMTPMessage(client, message)
}

func (s *smtpSender) implicitTLS(ctx context.Context, conn net.Conn) (net.Conn, error) {
	if s.config.TLSMode != SMTPModeTLS {
		return conn, nil
	}
	tlsConn := tls.Client(conn, s.tlsConfig())
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("mail: SMTP TLS handshake failed")
	}
	return tlsConn, nil
}

func (s *smtpSender) startTLS(client *smtp.Client) error {
	if s.config.TLSMode != SMTPModeStartTLS {
		return nil
	}
	if supported, _ := client.Extension("STARTTLS"); !supported {
		return fmt.Errorf("mail: SMTP server does not support required STARTTLS")
	}
	if err := client.StartTLS(s.tlsConfig()); err != nil {
		return fmt.Errorf("mail: SMTP STARTTLS handshake failed")
	}
	return nil
}

func (s *smtpSender) authenticate(client *smtp.Client) error {
	if s.config.Username == "" {
		return nil
	}
	if err := client.Auth(smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)); err != nil {
		return fmt.Errorf("mail: SMTP authentication failed")
	}
	return nil
}

func sendSMTPMessage(client *smtp.Client, message verificationEmail) error {
	if err := client.Mail(message.from); err != nil {
		return fmt.Errorf("mail: SMTP sender command failed")
	}
	if err := client.Rcpt(message.to); err != nil {
		return fmt.Errorf("mail: SMTP recipient command failed")
	}
	data, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: SMTP data command failed")
	}
	content, err := buildMIMEMessage(message)
	if err != nil {
		_ = data.Close()
		return err
	}
	if _, err := data.Write(content); err != nil {
		_ = data.Close()
		return fmt.Errorf("mail: SMTP message write failed")
	}
	if err := data.Close(); err != nil {
		return fmt.Errorf("mail: SMTP message acceptance failed")
	}
	return nil
}

func (s *smtpSender) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: s.config.Host,
		RootCAs:    s.rootCAs,
	}
}

func buildMIMEMessage(message verificationEmail) ([]byte, error) {
	from, err := mail.ParseAddress(message.from)
	if err != nil || from == nil || from.Name != "" || from.Address != message.from {
		return nil, fmt.Errorf("mail: MIME sender address is invalid")
	}
	to, err := mail.ParseAddress(message.to)
	if err != nil || to == nil || to.Name != "" || to.Address != message.to {
		return nil, fmt.Errorf("mail: MIME recipient address is invalid")
	}

	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	for _, part := range []struct {
		contentType string
		value       string
	}{
		{contentType: "text/plain; charset=UTF-8", value: message.text},
		{contentType: "text/html; charset=UTF-8", value: message.html},
	} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", part.contentType)
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		writer, err := multipartWriter.CreatePart(header)
		if err != nil {
			return nil, fmt.Errorf("mail: create MIME part")
		}
		quotedWriter := quotedprintable.NewWriter(writer)
		if _, err := quotedWriter.Write([]byte(part.value)); err != nil {
			return nil, fmt.Errorf("mail: write MIME part")
		}
		if err := quotedWriter.Close(); err != nil {
			return nil, fmt.Errorf("mail: close MIME part")
		}
	}
	if err := multipartWriter.Close(); err != nil {
		return nil, fmt.Errorf("mail: close MIME message")
	}
	contentType := mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": multipartWriter.Boundary()})
	if contentType == "" {
		return nil, fmt.Errorf("mail: format MIME content type")
	}
	var messageBytes bytes.Buffer
	messageBytes.WriteString("From: <" + from.Address + ">\r\n")
	messageBytes.WriteString("To: <" + to.Address + ">\r\n")
	messageBytes.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	messageBytes.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", message.subject) + "\r\n")
	messageBytes.WriteString("MIME-Version: 1.0\r\n")
	messageBytes.WriteString("Content-Type: " + contentType + "\r\n\r\n")
	_, _ = messageBytes.Write(body.Bytes())
	return messageBytes.Bytes(), nil
}

func urlParseHost(host string) (string, error) {
	parsed, err := url.Parse("//" + host)
	if err != nil || parsed.Host != host || parsed.Hostname() != host || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid SMTP host")
	}
	return parsed.Hostname(), nil
}
