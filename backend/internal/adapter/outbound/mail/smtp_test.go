package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type smtpTestServer struct {
	listener    net.Listener
	mode        string
	certificate tls.Certificate
	startTLS    bool
	blockAuth   bool
	messages    chan []byte
	authSeen    chan struct{}
	done        chan error
}

func newSMTPTestServer(t *testing.T, mode string, certificate tls.Certificate, startTLS, blockAuth bool) *smtpTestServer {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &smtpTestServer{
		listener:    listener,
		mode:        mode,
		certificate: certificate,
		startTLS:    startTLS,
		blockAuth:   blockAuth,
		messages:    make(chan []byte, 1),
		authSeen:    make(chan struct{}, 1),
		done:        make(chan error, 1),
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			server.done <- acceptErr
			return
		}
		if mode == SMTPModeTLS {
			tlsConn := tls.Server(conn, server.tlsConfig())
			if handshakeErr := tlsConn.HandshakeContext(ctx); handshakeErr != nil {
				_ = conn.Close()
				server.done <- handshakeErr
				return
			}
			conn = tlsConn
		}
		server.done <- server.serve(ctx, conn)
	}()
	return server
}

func (s *smtpTestServer) tlsConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{s.certificate}, MinVersion: tls.VersionTLS12}
}

func (s *smtpTestServer) serve(ctx context.Context, conn net.Conn) error {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	if err := smtpReply(writer, "220 smtp.example.org ESMTP ready"); err != nil {
		return err
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil
		}
		command := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO ") || strings.HasPrefix(upper, "HELO "):
			replies := []string{"250-smtp.example.org"}
			if s.startTLS && s.mode == SMTPModeStartTLS {
				replies = append(replies, "250-STARTTLS")
			}
			replies = append(replies, "250 AUTH PLAIN")
			if err := smtpReplies(writer, replies...); err != nil {
				return err
			}
		case upper == "STARTTLS":
			if err := smtpReply(writer, "220 Ready to start TLS"); err != nil {
				return err
			}
			tlsConn := tls.Server(conn, s.tlsConfig())
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				return err
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
			writer = bufio.NewWriter(conn)
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			var encoded string
			parts := strings.Fields(command)
			if len(parts) > 2 {
				encoded = parts[len(parts)-1]
			} else {
				if err := smtpReply(writer, "334 "); err != nil {
					return err
				}
				encodedLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return nil
				}
				encoded = strings.TrimSpace(encodedLine)
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || string(decoded) != "\x00synthetic-user\x00 synthetic password " {
				if err := smtpReply(writer, "535 Authentication failed"); err != nil {
					return err
				}
				continue
			}
			s.authSeen <- struct{}{}
			if s.blockAuth {
				_, _ = reader.ReadString('\n')
				return nil
			}
			if err := smtpReply(writer, "235 Authentication successful"); err != nil {
				return err
			}
		case strings.HasPrefix(upper, "MAIL FROM:") || strings.HasPrefix(upper, "RCPT TO:"):
			if err := smtpReply(writer, "250 Accepted"); err != nil {
				return err
			}
		case upper == "DATA":
			if err := smtpReply(writer, "354 End data with <CR><LF>.<CR><LF>"); err != nil {
				return err
			}
			var message bytes.Buffer
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return readErr
				}
				if dataLine == ".\r\n" {
					break
				}
				if strings.HasPrefix(dataLine, "..") {
					dataLine = dataLine[1:]
				}
				message.WriteString(dataLine)
			}
			s.messages <- message.Bytes()
			if err := smtpReply(writer, "250 Message accepted"); err != nil {
				return err
			}
		default:
			if err := smtpReply(writer, "250 Accepted"); err != nil {
				return err
			}
		}
	}
}

func smtpReply(writer *bufio.Writer, line string) error {
	if _, err := fmt.Fprintf(writer, "%s\r\n", line); err != nil {
		return err
	}
	return writer.Flush()
}

func smtpReplies(writer *bufio.Writer, lines ...string) error {
	for _, line := range lines {
		if err := smtpReply(writer, line); err != nil {
			return err
		}
	}
	return nil
}

func TestSMTPSenderUsesVerifiedTLSAndMultipartMIME(t *testing.T) {
	certificate, roots := testSMTPIdentity(t)
	for _, mode := range []string{SMTPModeStartTLS, SMTPModeTLS} {
		t.Run(mode, func(t *testing.T) {
			server := newSMTPTestServer(t, mode, certificate, true, false)
			cfg := SMTPConfig{
				Host: "localhost", Port: server.listener.Addr().(*net.TCPAddr).Port,
				Username: "synthetic-user", Password: " synthetic password ", TLSMode: mode,
			}
			sender, err := New(Config{
				Provider: ProviderSMTP,
				From:     "noreply@example.org",
				SMTP:     cfg,
				Timeout:  3 * time.Second,
			})
			require.NoError(t, err)
			sender.smtp, err = newSMTPSender(cfg, sender.timeout, func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", server.listener.Addr().String())
			}, roots)
			require.NoError(t, err)

			link := "https://example.org/verify&email#token=synthetic_token-123"
			require.NoError(t, sender.SendVerification(context.Background(), "player@example.invalid", link))
			wire := <-server.messages
			parsed, err := mail.ReadMessage(bytes.NewReader(wire))
			require.NoError(t, err)
			require.NotEmpty(t, parsed.Header.Get("Date"))
			subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
			require.NoError(t, err)
			require.Equal(t, "Подтвердите регистрацию в Task Per Minute", subject)
			mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
			require.NoError(t, err)
			require.Equal(t, "multipart/alternative", mediaType)
			parts := map[string]string{}
			multipartReader := multipart.NewReader(parsed.Body, params["boundary"])
			for {
				part, nextErr := multipartReader.NextRawPart()
				if nextErr == io.EOF {
					break
				}
				require.NoError(t, nextErr)
				contentType, _, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
				require.NoError(t, parseErr)
				require.Equal(t, "quoted-printable", part.Header.Get("Content-Transfer-Encoding"))
				content, readErr := io.ReadAll(quotedprintable.NewReader(part))
				require.NoError(t, readErr)
				parts[contentType] = string(content)
			}
			require.Contains(t, parts["text/plain"], link)
			require.Contains(t, parts["text/html"], "https://example.org/verify&amp;email#token=synthetic_token-123")
			select {
			case <-server.authSeen:
			case <-time.After(time.Second):
				t.Fatal("SMTP authentication was not observed")
			}
			select {
			case <-server.done:
			case <-time.After(time.Second):
				t.Fatal("SMTP server did not close after the message")
			}
		})
	}
}

func TestSMTPSenderRejectsServersWithoutSTARTTLS(t *testing.T) {
	certificate, roots := testSMTPIdentity(t)
	server := newSMTPTestServer(t, SMTPModeStartTLS, certificate, false, false)
	cfg := SMTPConfig{
		Host: "localhost", Port: server.listener.Addr().(*net.TCPAddr).Port,
		Username: "synthetic-user", Password: " synthetic password ", TLSMode: SMTPModeStartTLS,
	}
	sender, err := New(Config{Provider: ProviderSMTP, From: "noreply@example.org", SMTP: cfg, Timeout: time.Second})
	require.NoError(t, err)
	sender.smtp, err = newSMTPSender(cfg, sender.timeout, func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.listener.Addr().String())
	}, roots)
	require.NoError(t, err)
	err = sender.SendVerification(context.Background(), "player@example.invalid", "https://example.org/verify-email#token=synthetic_token")
	require.ErrorContains(t, err, "required STARTTLS")
	select {
	case <-server.authSeen:
		t.Fatal("SMTP client authenticated without STARTTLS")
	default:
	}
}

func TestSMTPSenderStopsWhenContextIsCanceled(t *testing.T) {
	certificate, roots := testSMTPIdentity(t)
	server := newSMTPTestServer(t, SMTPModeStartTLS, certificate, true, true)
	cfg := SMTPConfig{
		Host: "localhost", Port: server.listener.Addr().(*net.TCPAddr).Port,
		Username: "synthetic-user", Password: " synthetic password ", TLSMode: SMTPModeStartTLS,
	}
	sender, err := New(Config{Provider: ProviderSMTP, From: "noreply@example.org", SMTP: cfg, Timeout: 3 * time.Second})
	require.NoError(t, err)
	sender.smtp, err = newSMTPSender(cfg, sender.timeout, func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.listener.Addr().String())
	}, roots)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- sender.SendVerification(ctx, "player@example.invalid", "https://example.org/verify-email#token=synthetic_token")
	}()
	select {
	case <-server.authSeen:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("SMTP server did not receive authentication")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("SMTP send did not stop after context cancellation")
	}
	select {
	case <-server.done:
	case <-time.After(time.Second):
		t.Fatal("SMTP server did not observe the canceled connection")
	}
}

func testSMTPIdentity(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)
	parsedCertificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(parsedCertificate)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey, Leaf: parsedCertificate}, roots
}
