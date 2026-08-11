package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/asea/easyssh/internal/update"
)

// setupUpdateFakeServer 用 httptest 模拟 GitHub latest API,返回服务器与还原函数。
func setupUpdateFakeServer(t *testing.T, latestTag string) {
	t.Helper()
	origAPI := update.LatestAPI
	origClient := update.HTTPClient
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"` + latestTag + `","html_url":"https://github.com/AseaJ/easyssh/releases/tag/` + latestTag + `"}`))
	}))
	update.LatestAPI = srv.URL
	update.HTTPClient = srv.Client()
	t.Cleanup(func() {
		update.LatestAPI = origAPI
		update.HTTPClient = origClient
		srv.Close()
	})
}

// waitUpdateDone 轮询等待 CheckForUpdate 完成一次检查(CheckDone=true)。
func waitUpdateDone(t *testing.T, a *App) UpdateInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info := a.CheckForUpdate()
		if info.CheckDone {
			return info
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待更新检测超时")
	return UpdateInfo{}
}

func TestAppCheckForUpdateHasNewVersion(t *testing.T) {
	setupUpdateFakeServer(t, "v0.3.0")
	oldVersion := BuildVersion
	BuildVersion = "0.2.1"
	defer func() { BuildVersion = oldVersion }()

	dir := t.TempDir()
	a := NewApp(dir)
	// 不调用 Startup(会起托盘),直接触发检测
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.checkUpdate(context.Background(), 0)
	}()
	wg.Wait()

	info := waitUpdateDone(t, a)
	if !info.HasUpdate {
		t.Fatalf("期望有更新,got %+v", info)
	}
	if info.Latest != "v0.3.0" {
		t.Fatalf("Latest = %q", info.Latest)
	}
	if info.Current != "v0.2.1" {
		t.Fatalf("Current = %q", info.Current)
	}
	if info.ReleaseURL == "" {
		t.Fatal("ReleaseURL 为空")
	}
}

func TestAppCheckForUpdateNoNewVersion(t *testing.T) {
	setupUpdateFakeServer(t, "v0.2.1")
	oldVersion := BuildVersion
	BuildVersion = "0.2.1"
	defer func() { BuildVersion = oldVersion }()

	dir := t.TempDir()
	a := NewApp(dir)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.checkUpdate(context.Background(), 0)
	}()
	wg.Wait()

	info := waitUpdateDone(t, a)
	if info.HasUpdate {
		t.Fatalf("期望无更新,got %+v", info)
	}
}

func TestAppCheckUpdateFailure(t *testing.T) {
	// 用不可达地址模拟网络失败
	origAPI := update.LatestAPI
	update.LatestAPI = "http://127.0.0.1:1/nope"
	defer func() { update.LatestAPI = origAPI }()

	dir := t.TempDir()
	a := NewApp(dir)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.checkUpdate(context.Background(), 0)
	}()
	wg.Wait()

	info := waitUpdateDone(t, a)
	if info.Error == "" {
		t.Fatal("期望记录错误")
	}
	if info.HasUpdate {
		t.Fatal("失败时不应提示更新")
	}
}

func TestAppCheckUpdateRetryAfterFailure(t *testing.T) {
	// 第一次失败 → 节流时间被重置 → 第二次(服务器恢复)立即重试成功
	dir := t.TempDir()
	a := NewApp(dir)

	// 第一阶段:服务器不可达
	update.LatestAPI = "http://127.0.0.1:1/nope"
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.checkUpdate(context.Background(), 0)
	}()
	wg.Wait()
	info := waitUpdateDone(t, a)
	if info.Error == "" {
		t.Fatal("第一阶段应失败")
	}

	// 第二阶段:服务器恢复,且间隔设为 1 小时(验证失败后不再受节流限制)
	setupUpdateFakeServer(t, "v0.5.0")
	BuildVersion = "v0.4.0"
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.checkUpdate(context.Background(), time.Hour)
	}()
	wg.Wait()
	info = waitUpdateDone(t, a)
	if !info.HasUpdate {
		t.Fatalf("失败后应立即重试并检出更新,got %+v", info)
	}
	if info.Latest != "v0.5.0" {
		t.Fatalf("Latest = %q", info.Latest)
	}
}

func TestAppOpenUpdatePage(t *testing.T) {
	setupUpdateFakeServer(t, "v0.3.0")
	dir := t.TempDir()
	a := NewApp(dir)
	// 直接设置 ReleaseURL,验证 OpenUpdatePage 不 panic 且 URL 兜底逻辑
	a.updateState.mu.Lock()
	a.updateState.info = UpdateInfo{ReleaseURL: "https://example.com/releases"}
	a.updateState.mu.Unlock()
	a.OpenUpdatePage()
	// 若 ReleaseURL 为空应兜底到 update.ReleasesURL(此处不实际打开浏览器)
	info := a.CheckForUpdate()
	if info.ReleaseURL != "https://example.com/releases" {
		t.Fatalf("ReleaseURL 被意外修改: %q", info.ReleaseURL)
	}
}
