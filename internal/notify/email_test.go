package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/asea/easyssh/internal/config"
)

func TestEmailNotify(t *testing.T) {
	cfg := config.SMTPConfig{Host: "smtp.example.com", Port: 465, User: "alert@example.com", Pass: "secret", To: []string{"ops@example.com", "admin@example.com"}}
	var sent []byte
	var sentCfg config.SMTPConfig
	e, err := NewEmail(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.send = func(c config.SMTPConfig, msg []byte) error {
		sentCfg = c
		sent = msg
		return nil
	}
	ev := Event{Level: "error", Subject: "签发失败", Message: "boom", Entry: "a", Time: time.Now()}
	if err := e.Notify(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	body := string(sent)
	for _, want := range []string{"From: easyssh <alert@example.com>", "To: ops@example.com, admin@example.com", "Content-Type: text/plain; charset=utf-8", "Subject: =?utf-8?", "boom", "级别: error"} {
		if !strings.Contains(body, want) {
			t.Errorf("邮件内容缺少 %q:\n%s", want, body)
		}
	}
	if sentCfg.Port != 465 {
		t.Errorf("端口 = %d", sentCfg.Port)
	}
}

func TestEmailConfigInvalid(t *testing.T) {
	if _, err := NewEmail(config.SMTPConfig{Host: "h", User: "u"}); err == nil {
		t.Fatal("缺少 pass/to 应报错")
	}
}

func TestEmailHostRejectsMailboxAddress(t *testing.T) {
	cfg := config.SMTPConfig{Host: "1973135690@qq.com", Port: 465, User: "1973135690@qq.com", Pass: "x", To: []string{"a@b.com"}}
	_, err := NewEmail(cfg)
	if err == nil {
		t.Fatal("smtp_host 填成邮箱地址应报错")
	}
	if !strings.Contains(err.Error(), "SMTP 服务器地址") {
		t.Errorf("错误信息应提示正确的填法,实际: %v", err)
	}
}
