package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-zs/internal/certmgr"
	"go-zs/internal/storage"
	"go-zs/internal/testutil"
)

// writeTestConfig 创建带一个条目的配置目录。
func writeTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	certDir := filepath.ToSlash(filepath.Join(dir, "certs", "example"))
	cfg := `
ca:
  server: https://acme-staging-v02.api.letsencrypt.org/directory
  email: ops@example.com
  account_key: ./data/account.key
certificates:
  - name: example
    domains: [example.com]
    challenge: http-01
    storage:
      dir: ` + certDir + `
    deploy:
      - type: nginx
        reload_cmd: nginx -s reload
`
	if err := os.WriteFile(filepath.Join(dir, "go-zs.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// saveTestCert 在指定存储目录落一张测试证书。
func saveTestCert(t *testing.T, dir string) *certmgr.Bundle {
	t.Helper()
	store, err := storage.NewFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	leaf, key := testutil.GenSelfSigned([]string{"example.com"}, time.Now().Add(60*24*time.Hour))
	b := &certmgr.Bundle{
		Name:          "example",
		Domains:       []string{"example.com"},
		LeafPEM:       leaf,
		FullchainPEM:  leaf,
		PrivateKeyPEM: key,
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      time.Now().Add(60 * 24 * time.Hour),
		Fingerprint:   certmgr.FingerprintOf(leaf),
	}
	if err := store.Save(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAppListCertificatesNotIssued(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	if a.lastError != "" {
		t.Fatalf("配置加载失败: %s", a.lastError)
	}
	certs, err := a.ListCertificates()
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 1 {
		t.Fatalf("证书数 = %d", len(certs))
	}
	if certs[0].Status != "未签发" {
		t.Errorf("未签发状态错误: %s", certs[0].Status)
	}
}

func TestAppListCertificatesIssued(t *testing.T) {
	dir := writeTestConfig(t)
	saveTestCert(t, filepath.Join(dir, "certs", "example"))

	a := NewApp(dir)
	a.Startup(context.Background())
	certs, err := a.ListCertificates()
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 1 {
		t.Fatalf("证书数 = %d", len(certs))
	}
	c := certs[0]
	if c.Status != "fresh" {
		t.Errorf("状态 = %s,期望 fresh", c.Status)
	}
	if c.RemainDays < 58 || c.RemainDays > 60 {
		t.Errorf("剩余天数 = %d,期望约 60", c.RemainDays)
	}
	if c.Deployed {
		t.Error("配置含 nginx 部署目标且未部署,Deployed 应为 false")
	}

	ov, err := a.GetOverview()
	if err != nil {
		t.Fatal(err)
	}
	if ov.Total != 1 || ov.Healthy != 1 {
		t.Errorf("概览错误: total=%d healthy=%d", ov.Total, ov.Healthy)
	}
}

func TestAppOverviewExpiring(t *testing.T) {
	dir := writeTestConfig(t)
	// 快过期的证书 → renewing
	store, _ := storage.NewFS(filepath.Join(dir, "certs", "example"))
	leaf, key := testutil.GenSelfSigned([]string{"example.com"}, time.Now().Add(10*24*time.Hour))
	b := &certmgr.Bundle{
		Name:          "example",
		Domains:       []string{"example.com"},
		LeafPEM:       leaf,
		FullchainPEM:  leaf,
		PrivateKeyPEM: key,
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      time.Now().Add(10 * 24 * time.Hour),
		Fingerprint:   certmgr.FingerprintOf(leaf),
	}
	store.Save(context.Background(), b)

	a := NewApp(dir)
	a.Startup(context.Background())
	ov, err := a.GetOverview()
	if err != nil {
		t.Fatal(err)
	}
	if ov.Expiring != 1 {
		t.Errorf("待续期数 = %d,期望 1", ov.Expiring)
	}
}

func TestAppReloadConfig(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	// 破坏配置 → reload 应报错
	os.WriteFile(filepath.Join(dir, "go-zs.yaml"), []byte("certificates: []"), 0o600)
	if _, err := a.ReloadConfig(); err == nil {
		t.Fatal("空证书列表配置应校验失败")
	}
}

// TestConcurrentAccess 验证自动调度(背景 goroutine)与 Wails 方法并发访问不产生死锁/崩溃。
// 注:本机无 cgo,-race 不可用,此测试至少覆盖死锁与明显数据竞争导致的 panic。
func TestConcurrentAccess(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := a.GetConfig(); err != nil {
						t.Errorf("GetConfig: %v", err)
					}
					if _, err := a.GetOverview(); err != nil {
						t.Errorf("GetOverview: %v", err)
					}
					if _, err := a.ListCertificates(); err != nil {
						t.Errorf("ListCertificates: %v", err)
					}
					_, _ = a.ReloadConfig()
				}
			}
		}()
	}
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestNotifyConfigMissing(t *testing.T) {
	dir := writeTestConfig(t) // 无 smtp 配置
	a := NewApp(dir)
	a.Startup(context.Background())
	_, err := a.TestNotify()
	if err == nil {
		t.Fatal("未配置 SMTP 时 TestNotify 应报错")
	}
	if !strings.Contains(err.Error(), "SMTP") {
		t.Errorf("错误应提示 SMTP 配置缺失,实际: %v", err)
	}
}

func TestRunCheckNoSideEffect(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	msg, err := a.RunCheck()
	if err != nil {
		t.Fatalf("RunCheck 失败: %v", err)
	}
	if !strings.Contains(msg, "检查完成") {
		t.Errorf("RunCheck 返回文案异常: %s", msg)
	}
}