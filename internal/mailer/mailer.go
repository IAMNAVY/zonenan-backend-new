package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"
)

// Mailer 发送邮件(验证码等)。
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// New 按环境变量选择实现:
//
//	MAIL_PROVIDER=smtp   → SMTP(需 SMTP_HOST/PORT/USER/PASS/FROM)
//	MAIL_PROVIDER=resend → Resend(需 RESEND_API_KEY/MAIL_FROM)  [见 resend.go]
//	其他/未配置          → noop(dev:打日志,不真发)
func New() Mailer {
	switch strings.ToLower(os.Getenv("MAIL_PROVIDER")) {
	case "smtp":
		return &smtpMailer{
			host: os.Getenv("SMTP_HOST"),
			port: getenv("SMTP_PORT", "465"),
			user: os.Getenv("SMTP_USER"),
			pass: os.Getenv("SMTP_PASS"),
			from: getenv("MAIL_FROM", os.Getenv("SMTP_USER")),
		}
	case "resend":
		return &resendMailer{apiKey: os.Getenv("RESEND_API_KEY"), from: os.Getenv("MAIL_FROM")}
	default:
		return &noopMailer{}
	}
}

type noopMailer struct{}

func (m *noopMailer) Send(_ context.Context, to, subject, body string) error {
	log.Printf("[mailer:noop] to=%s subject=%q body=%q (未配置发信,dev 模式)", to, subject, body)
	return nil
}

type smtpMailer struct{ host, port, user, pass, from string }

func (m *smtpMailer) Send(_ context.Context, to, subject, body string) error {
	if m.host == "" {
		return fmt.Errorf("SMTP_HOST 未配置")
	}
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n%s", m.from, to, subject, body))
	auth := smtp.PlainAuth("", m.user, m.pass, m.host)
	// 465 = 隐式 TLS(SendMail 只会 STARTTLS,不兼容);其余端口(587 等)走 SendMail。
	if m.port == "465" {
		return m.sendImplicitTLS(to, msg, auth)
	}
	return smtp.SendMail(m.host+":"+m.port, auth, m.from, []string{to}, msg)
}

// sendImplicitTLS 在 465 端口用 tls.Dial 建立隐式 TLS 连接再投递。
func (m *smtpMailer) sendImplicitTLS(to string, msg []byte, auth smtp.Auth) error {
	addr := m.host + ":" + m.port
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.host})
	if err != nil {
		return err
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return err
	}
	defer c.Quit()
	if err := c.Auth(auth); err != nil {
		return err
	}
	if err := c.Mail(m.from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		return err
	}
	return wc.Close()
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
