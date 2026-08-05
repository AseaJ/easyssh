package notify

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoggerNotifier(t *testing.T) {
	var buf strings.Builder
	l := NewLogger(log.New(&buf, "", 0))
	err := l.Notify(context.Background(), Event{
		Level: "error", Subject: "签发失败", Message: "boom", Entry: "a",
		Time: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "签发失败") {
		t.Errorf("日志内容异常: %s", buf.String())
	}
}

func TestWebhookNotifier(t *testing.T) {
	var got Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("JSON 解析失败: %v", err)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	w := NewWebhook(srv.URL)
	ev := Event{Level: "warn", Subject: "部署失败", Message: "reload err", Entry: "b", Time: time.Now()}
	if err := w.Notify(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got.Subject != "部署失败" || got.Entry != "b" {
		t.Errorf("接收事件异常: %+v", got)
	}
}

func TestWebhookNotifierHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	w := NewWebhook(srv.URL)
	err := w.Notify(context.Background(), Event{Subject: "x"})
	if err == nil {
		t.Fatal("500 响应应报错")
	}
}
