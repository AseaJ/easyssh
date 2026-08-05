package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net/smtp"
	"strings"
	"time"

	"github.com/asea/easyssh/internal/config"
)

// Email 通过 SMTP 发送告警邮件。
// 支持 465(SSL)与 587/25(STARTTLS);密码建议用邮箱授权码({{env:VAR}} 引用)。
type Email struct {
	cfg  config.SMTPConfig
	send func(cfg config.SMTPConfig, msg []byte) error // 可注入,便于测试
}

func NewEmail(cfg config.SMTPConfig) (*Email, error) {
	if cfg.Host == "" || cfg.User == "" || cfg.Pass == "" || len(cfg.To) == 0 {
		return nil, fmt.Errorf("smtp 配置需要 host/user/pass/to")
	}
	if strings.Contains(cfg.Host, "@") {
		return nil, fmt.Errorf("smtp_host 是 SMTP 服务器地址(如 smtp.qq.com),不能填邮箱地址: %q", cfg.Host)
	}
	if cfg.Port == 0 {
		cfg.Port = 465
	}
	return &Email{cfg: cfg, send: SendSMTP}, nil
}

func (e *Email) Notify(_ context.Context, ev Event) error {
	msg := buildMail(e.cfg, ev)
	return e.send(e.cfg, msg)
}

// BuildTestMail 构造一封固定内容的测试邮件(供通知设置页测试 SMTP 链路)。
func BuildTestMail(user string, to []string) []byte {
	return buildMail(config.SMTPConfig{User: user, To: to}, Event{
		Level:   "info",
		Kind:    "test",
		Subject: "测试通知",
		Message: "这是一封来自 easyssh 的测试邮件。如果你收到了这封邮件,说明 SMTP 通知配置可用。",
		Entry:   "easyssh",
		Time:    time.Now(),
	})
}

// buildMail 构造 MIME 邮件(UTF-8,主题 base64 编码)。
func buildMail(cfg config.SMTPConfig, ev Event) []byte {
	subject := mime.QEncoding.Encode("utf-8", fmt.Sprintf("[easyssh] %s", ev.Subject))
	body := fmt.Sprintf("时间: %s\n级别: %s\n条目: %s\n\n%s\n",
		ev.Time.Format("2006-01-02 15:04:05"), ev.Level, ev.Entry, ev.Message)
	var sb strings.Builder
	sb.WriteString("From: easyssh <" + cfg.User + ">\r\n")
	sb.WriteString("To: " + strings.Join([]string(cfg.To), ", ") + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

// SendSMTP 发送邮件:465 走 SSL,其他端口走 STARTTLS(由 net/smtp.SendMail 自动协商)。
func SendSMTP(cfg config.SMTPConfig, msg []byte) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	if cfg.Port == 465 {
		return sendSMTPSSL(addr, cfg, auth, msg)
	}
	return smtp.SendMail(addr, auth, cfg.User, []string(cfg.To), msg)
}

// sendSMTPSSL 通过 TLS 直连(465)发送。
func sendSMTPSSL(addr string, cfg config.SMTPConfig, auth smtp.Auth, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host})
	if err != nil {
		return fmt.Errorf("TLS 连接失败: %w", err)
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP 认证失败: %w", err)
	}
	if err := client.Mail(cfg.User); err != nil {
		return err
	}
	for _, rcpt := range cfg.To {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
