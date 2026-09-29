// Package mailer delivers transactional email.
//
// It replaces the old in-memory mailbox, which was not a mailbox at all but a
// shared, world-readable map served over GET /inbox at a fixed address. Any
// authenticated user could read every message, which turned the password-reset
// flow into a one-request account takeover.
//
// Nothing here is ever exposed over HTTP. A reset token reaches the user only
// through their own inbox.
package mailer

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Message is a single outbound email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer sends transactional email. Send reports delivery failures so the
// caller can decide what to do: a reset mail that silently vanished leaves the
// user permanently locked out with no way to tell why.
type Mailer interface {
	Send(Message) error
}

// From builds the Mailer appropriate for the environment: SMTP when a host and
// sender are configured, otherwise a backend that logs.
func From(smtpCfg SMTPConfig) Mailer {
	if !smtpCfg.Enabled() {
		slog.Info("mailer: SMTP not configured, falling back to the log backend",
			"consequence", "password reset links are written to the application log instead of being emailed")
		return Log()
	}
	return &SMTPMailer{cfg: smtpCfg}
}

// SMTPConfig carries the settings needed to reach an SMTP relay.
type SMTPConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// Enabled reports whether enough configuration is present to dial out.
func (c SMTPConfig) Enabled() bool { return c.Host != "" && c.From != "" }

// SMTPMailer delivers over SMTP, with opportunistic STARTTLS.
type SMTPMailer struct {
	cfg SMTPConfig
}

func (m *SMTPMailer) Send(msg Message) error {
	if err := validateRecipient(msg.To); err != nil {
		return err
	}

	addr := net.JoinHostPort(m.cfg.Host, fmt.Sprint(m.cfg.Port))
	msgBody := formatMessage(m.cfg.From, msg)

	// Implicit TLS on the submission port, otherwise upgrade with STARTTLS.
	var client *smtp.Client
	if m.cfg.Port == 465 {
		// net/smtp has no DialTLS helper: dial the TLS connection ourselves
		// and hand the already-encrypted conn to smtp.NewClient.
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return fmt.Errorf("smtp dial (implicit tls): %w", err)
		}
		c, err := smtp.NewClient(conn, m.cfg.Host)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("smtp new client: %w", err)
		}
		client = c
	} else {
		c, err := smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("smtp dial: %w", err)
		}
		if err := c.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = c.Close()
			return fmt.Errorf("smtp starttls: %w", err)
		}
		client = c
	}
	defer func() { _ = client.Close() }()

	if m.cfg.User != "" {
		auth := smtp.PlainAuth("", m.cfg.User, m.cfg.Password, m.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := wc.Write([]byte(msgBody)); err != nil {
		_ = wc.Close()
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	return client.Quit()
}

// LogMailer writes the message to the application log instead of sending it.
// Intended for local development, where standing up a relay is not worth it.
type LogMailer struct{}

func Log() *LogMailer { return &LogMailer{} }

func (l *LogMailer) Send(msg Message) error {
	slog.Warn("mailer: log backend used, message was NOT delivered by email",
		"to", msg.To,
		"subject", msg.Subject)
	// The body goes to the log at debug level so a reset link is not sitting in
	// the default log stream, while remaining reachable when developing.
	slog.Debug("mailer: message body", "to", msg.To, "body", msg.Body)
	return nil
}

// validateRecipient rejects header-injection attempts. Without this, a
// newline in the address turns into extra headers, including a BCC list, and
// the reset token ends up in someone else's mailbox.
func validateRecipient(addr string) error {
	if addr == "" {
		return fmt.Errorf("smtp: empty recipient")
	}
	if strings.ContainsAny(addr, "\r\n") {
		return fmt.Errorf("smtp: recipient contains a line break")
	}
	if !strings.Contains(addr, "@") {
		return fmt.Errorf("smtp: recipient %q is not a valid address", addr)
	}
	return nil
}

// formatMessage builds a minimal RFC 5322 message. The body is intentionally
// 7bit rather than base64: a base64 body is a classic way to smuggle a payload
// past naive scanners, and there is no reason to use it here.
func formatMessage(from string, msg Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(normalizeNewlines(msg.Body))
	return b.String()
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}
