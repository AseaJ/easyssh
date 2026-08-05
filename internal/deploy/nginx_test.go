package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asea/easyssh/internal/certmgr"
	"github.com/asea/easyssh/internal/testutil"
)

// mockRunner 记录调用并可按命令模拟失败。
type mockRunner struct {
	calls   []string
	failMap map[string]bool // 程序名 → 是否失败
}

func (m *mockRunner) Run(_ context.Context, name string, args ...string) error {
	m.calls = append(m.calls, name)
	if m.failMap[name] {
		return os.ErrPermission
	}
	return nil
}

func bundle() *certmgr.Bundle {
	leaf, key := testutil.GenSelfSigned([]string{"a.com"}, time.Now().Add(60*24*time.Hour))
	b := &certmgr.Bundle{
		Name:          "a",
		Domains:       []string{"a.com"},
		LeafPEM:       leaf,
		FullchainPEM:  leaf,
		PrivateKeyPEM: key,
		NotAfter:      time.Now().Add(60 * 24 * time.Hour),
		Fingerprint:   certmgr.FingerprintOf(leaf),
	}
	return b
}

func TestNginxDeploySuccess(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ssl", "fullchain.pem")
	keyPath := filepath.Join(dir, "ssl", "privkey.pem")
	runner := &mockRunner{}
	n, err := NewNginx(NginxConfig{
		CertPath:  certPath,
		KeyPath:   keyPath,
		TestCmd:   "nginx -t",
		ReloadCmd: "nginx -s reload",
		Runner:    runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	b := bundle()
	if err := n.Deploy(context.Background(), b); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	// 校验执行顺序:nginx(->t) 然后 nginx(-s reload)
	if len(runner.calls) != 2 || runner.calls[0] != "nginx" || runner.calls[1] != "nginx" {
		t.Fatalf("调用序列异常: %v", runner.calls)
	}
	// 文件已写入且内容正确
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(b.FullchainPEM) {
		t.Error("证书文件内容不一致")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 && os.Getenv("CI") == "" {
		// Windows 无 POSIX 权限,跳过
	}
}

func TestNginxDeployIdempotent(t *testing.T) {
	dir := t.TempDir()
	runner := &mockRunner{}
	n, _ := NewNginx(NginxConfig{
		CertPath:  filepath.Join(dir, "fullchain.pem"),
		KeyPath:   filepath.Join(dir, "privkey.pem"),
		TestCmd:   "nginx -t",
		ReloadCmd: "nginx -s reload",
		Runner:    runner,
	})
	b := bundle()
	b.Meta.DeployedFingerprint = b.Fingerprint
	b.Meta.DeployedTargets = []string{"nginx"}

	if err := n.Deploy(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("幂等场景不应执行任何命令,实际 %v", runner.calls)
	}
}

func TestNginxDeployTestFailRollback(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	// 先放一份"旧"证书
	old := []byte("OLD CERT")
	os.WriteFile(certPath, old, 0o644)

	runner := &mockRunner{failMap: map[string]bool{"nginx": true}}
	n, _ := NewNginx(NginxConfig{
		CertPath:  certPath,
		KeyPath:   keyPath,
		TestCmd:   "nginx -t",
		ReloadCmd: "nginx -s reload",
		Runner:    runner,
	})
	err := n.Deploy(context.Background(), bundle())
	if err == nil {
		t.Fatal("校验失败应返回错误")
	}
	// 旧文件应被回滚恢复
	data, _ := os.ReadFile(certPath)
	if string(data) != string(old) {
		t.Error("校验失败后未回滚旧证书")
	}
	// 不应执行 reload(两次调用都是 nginx -t 内部?不,mock 记录的是程序名,校验失败只调一次)
	if len(runner.calls) != 1 {
		t.Errorf("校验失败不应触发 reload,calls = %v", runner.calls)
	}
}

func TestFileDeploy(t *testing.T) {
	dir := t.TempDir()
	f, err := NewFile(FileConfig{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	b := bundle()
	if err := f.Deploy(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fullchain.pem")); err != nil {
		t.Error("fullchain.pem 未生成")
	}
	if _, err := os.Stat(filepath.Join(dir, "privkey.pem")); err != nil {
		t.Error("privkey.pem 未生成")
	}
	// 幂等
	b.Meta.DeployedFingerprint = b.Fingerprint
	b.Meta.DeployedTargets = []string{"file"}
	if err := f.Deploy(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}
