// Package notify 提供告警通知:默认写日志,可配置 webhook 推送。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Event 是一次通知事件。
type Event struct {
	Level   string    `json:"level"`          // info | warn | error
	Kind    string    `json:"kind,omitempty"` // expiring | success | failure | info(事件开关过滤用)
	Subject string    `json:"subject"`
	Message string    `json:"message"`
	Entry   string    `json:"entry,omitempty"`
	Time    time.Time `json:"time"`
}

// Notifier 是告警通知抽象。
type Notifier interface {
	Notify(ctx context.Context, e Event) error
}

// Logger 是默认通知器:写入日志。
type Logger struct {
	log *log.Logger
}

func NewLogger(logger *log.Logger) *Logger {
	if logger == nil {
		logger = log.Default()
	}
	return &Logger{log: logger}
}

func (l *Logger) Notify(_ context.Context, e Event) error {
	kind := e.Kind
	if kind == "" {
		kind = "default"
	}
	l.log.Printf("[notify:%s/%s] %s: %s (entry=%s)", e.Level, kind, e.Subject, e.Message, e.Entry)
	return nil
}

// Webhook 通过 HTTP POST 推送告警(JSON)。
type Webhook struct {
	URL    string
	Client *http.Client
}

func NewWebhook(url string) *Webhook {
	return &Webhook{URL: url, Client: &http.Client{Timeout: 10 * time.Second}}
}

func (w *Webhook) Notify(ctx context.Context, e Event) error {
	if w.URL == "" {
		return nil
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.Client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook 推送失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook 返回状态 %d", resp.StatusCode)
	}
	return nil
}
