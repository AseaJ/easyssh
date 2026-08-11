package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeReleaseJSON 构造一个最小可用的 release API 响应。
func fakeReleaseJSON(tag string, withWindows bool) string {
	var assets string
	if withWindows {
		assets = `,"assets":[{"name":"easyssh-windows-amd64.exe","browser_download_url":"https://github.com/AseaJ/easyssh/releases/download/` + tag + `/easyssh-windows-amd64.exe"}]`
	}
	return `{"tag_name":"` + tag + `","html_url":"https://github.com/AseaJ/easyssh/releases/tag/` + tag + `"` + assets + `}`
}

func TestLatest(t *testing.T) {
	orig := LatestAPI
	origClient := HTTPClient
	defer func() { LatestAPI = orig; HTTPClient = origClient }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fakeReleaseJSON("v0.3.0", true)))
	}))
	defer srv.Close()
	LatestAPI = srv.URL
	HTTPClient = srv.Client()

	t.Run("有新版本", func(t *testing.T) {
		info, err := Latest(context.Background(), "v0.2.1")
		if err != nil {
			t.Fatalf("Latest err: %v", err)
		}
		if !info.HasUpdate {
			t.Fatal("期望 HasUpdate=true")
		}
		if info.Latest != "v0.3.0" {
			t.Fatalf("Latest = %q", info.Latest)
		}
		if info.ReleaseURL == "" {
			t.Fatal("ReleaseURL 为空")
		}
	})
	t.Run("无更新", func(t *testing.T) {
		info, err := Latest(context.Background(), "v0.3.0")
		if err != nil {
			t.Fatalf("Latest err: %v", err)
		}
		if info.HasUpdate {
			t.Fatal("期望 HasUpdate=false")
		}
	})
	t.Run("当前版本未知", func(t *testing.T) {
		info, err := Latest(context.Background(), "")
		if err != nil {
			t.Fatalf("Latest err: %v", err)
		}
		if info.HasUpdate {
			t.Fatal("当前版本未知时不应提示更新")
		}
	})
	t.Run("当前版本非法(非 semver)", func(t *testing.T) {
		info, err := Latest(context.Background(), "not-a-version")
		if err != nil {
			t.Fatalf("Latest err: %v", err)
		}
		if info.HasUpdate {
			t.Fatal("当前版本非法时不应提示更新")
		}
	})
	t.Run("当前版本为预发布且较旧", func(t *testing.T) {
		// v0.2.1-beta.1 < v0.3.0,应提示更新(预发布也是合法 semver)
		info, err := Latest(context.Background(), "v0.2.1-beta.1")
		if err != nil {
			t.Fatalf("Latest err: %v", err)
		}
		if !info.HasUpdate {
			t.Fatal("预发布旧版本应提示更新")
		}
	})
	t.Run("API 错误", func(t *testing.T) {
		LatestAPI = "http://127.0.0.1:1/nope" // 连接失败
		if _, err := Latest(context.Background(), "v0.2.1"); err == nil {
			t.Fatal("期望网络错误")
		}
	})
	t.Run("HTTP 非 200", func(t *testing.T) {
		srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv2.Close()
		LatestAPI = srv2.URL
		if _, err := Latest(context.Background(), "v0.2.1"); err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("期望 403 错误,got: %v", err)
		}
	})
}
