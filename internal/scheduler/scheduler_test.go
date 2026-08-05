package scheduler

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go-zs/internal/acme"
	"go-zs/internal/certmgr"
	"go-zs/internal/config"
	"go-zs/internal/deploy"
	"go-zs/internal/notify"
	"go-zs/internal/storage"
	"go-zs/internal/testutil"
)

// mockProv 是测试用签发源,实现 acme.Provisioner。
type mockProv struct {
	fail   bool
	calls  int
	bundle *certmgr.Bundle
}

func (m *mockProv) Name() string { return "mock" }

func (m *mockProv) Ensure(_ context.Context, domains []string) (*certmgr.Bundle, error) {
	m.calls++
	if m.fail {
		return nil, errors.New("mock 签发失败")
	}
	return testBundle("", domains, time.Now().Add(90*24*time.Hour)), nil
}

// testBundle 构造测试 Bundle。
func testBundle(name string, domains []string, notAfter time.Time) *certmgr.Bundle {
	leaf, key := testutil.GenSelfSigned(domains, notAfter)
	return &certmgr.Bundle{
		Name:          name,
		Domains:       domains,
		LeafPEM:       leaf,
		FullchainPEM:  leaf,
		PrivateKeyPEM: key,
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      notAfter,
		Fingerprint:   certmgr.FingerprintOf(leaf),
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		CA: config.CAConfig{Server: "https://acme.example.test", Email: "t@t.test", AccountKey: "./data/account.key"},
		Certificates: []config.CertificateConfig{
			{
				Name:      "a",
				Domains:   []string{"a.com"},
				Challenge: "http-01",
				Storage:   config.StorageConfig{Dir: t.TempDir()},
				Deploy: []config.DeployConfig{
					{Type: "nginx", ReloadCmd: "nginx -s reload"},
				},
			},
		},
		Schedule: config.DefaultSchedule(),
	}
}

func newTestScheduler(t *testing.T, cfg *config.Config, factory ProvisionerFactory) *Scheduler {
	t.Helper()
	s := New(cfg, factory, log.New(io.Discard, "", 0))
	// 默认注入成功的 mock deployer,使不关心部署的测试直接通过
	s.deployFactory = func(c config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		return &mockDeployer{name: c.Type}, nil
	}
	return s
}

func TestIssueSuccess(t *testing.T) {
	cfg := testConfig(t)
	prov := &mockProv{}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})

	reports := s.RunOnce(context.Background())
	if len(reports) != 1 {
		t.Fatalf("报告数 = %d,期望 1", len(reports))
	}
	r := reports[0]
	if !r.OK {
		t.Fatalf("首次签发失败: %s", r.Error)
	}
	if r.Action != certmgr.ActionIssue {
		t.Errorf("动作 = %s,期望 issue", r.Action)
	}
	store, err := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Load(context.Background(), "a")
	if err != nil || b == nil {
		t.Fatalf("证书未落盘: %v", err)
	}
	if b.Meta.FailCount != 0 {
		t.Errorf("成功后 FailCount = %d", b.Meta.FailCount)
	}
}

func TestRunOnceFailureBackoff(t *testing.T) {
	cfg := testConfig(t)
	prov := &mockProv{fail: true}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})

	reports := s.RunOnce(context.Background())
	if reports[0].OK {
		t.Fatal("失败场景不应 OK")
	}
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	meta := readMeta(t, store, "a")
	if meta.FailCount != 1 {
		t.Errorf("FailCount = %d,期望 1", meta.FailCount)
	}
	wantNext := time.Now().Add(cfg.Schedule.RetryBackoff[0].Std())
	if diff := meta.NextRetryAt.Sub(wantNext); diff > time.Minute || diff < -time.Minute {
		t.Errorf("NextRetryAt = %v,期望约 %v", meta.NextRetryAt, wantNext)
	}
	if meta.LastError == "" {
		t.Error("LastError 未记录")
	}
}

func TestSkipDuringBackoff(t *testing.T) {
	cfg := testConfig(t)
	prov := &mockProv{fail: true}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	s.RunOnce(context.Background()) // 失败,进入退避

	// 立即再跑:未到重试时间应 skip,不再调用 Ensure
	s2 := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	reports := s2.RunOnce(context.Background())
	if reports[0].Action != certmgr.ActionSkip {
		t.Fatalf("退避中应 skip,实际 %s(%s)", reports[0].Action, reports[0].Error)
	}
	if prov.calls != 1 {
		t.Errorf("退避中不应调用 Ensure,calls = %d", prov.calls)
	}
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	meta := readMeta(t, store, "a")
	if meta.FailCount != 1 {
		t.Errorf("退避中不应重复计数,FailCount = %d", meta.FailCount)
	}
}

func TestRenewAfterExpiry(t *testing.T) {
	cfg := testConfig(t)
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	old := testBundle("a", []string{"a.com"}, time.Now().Add(5*24*time.Hour))
	if err := store.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}

	prov := &mockProv{}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	reports := s.RunOnce(context.Background())
	if reports[0].Action != certmgr.ActionRenew {
		t.Fatalf("快到期应 renew,实际 %s(%s)", reports[0].Action, reports[0].Error)
	}
	if !reports[0].OK {
		t.Fatalf("renew 失败: %s", reports[0].Error)
	}
	newB, _ := store.Load(context.Background(), "a")
	if newB.Fingerprint == old.Fingerprint {
		t.Error("renew 后指纹未变,证书未替换")
	}
}

func TestReissueOnSANChange(t *testing.T) {
	cfg := testConfig(t)
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	// 已有证书,但 SAN 与配置不一致
	old := testBundle("a", []string{"old.com"}, time.Now().Add(60*24*time.Hour))
	if err := store.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}

	prov := &mockProv{}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	reports := s.RunOnce(context.Background())
	if reports[0].Action != certmgr.ActionReissue {
		t.Fatalf("SAN 变化应 reissue,实际 %s", reports[0].Action)
	}
	newB, _ := store.Load(context.Background(), "a")
	if newB.Fingerprint == old.Fingerprint {
		t.Error("reissue 后指纹未变")
	}
}

func TestForceRenew(t *testing.T) {
	cfg := testConfig(t)
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	// 证书还有效(60 天),强制续期应仍重新签发
	old := testBundle("a", []string{"a.com"}, time.Now().Add(60*24*time.Hour))
	if err := store.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}

	prov := &mockProv{}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	rep, err := s.ForceRenew(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK {
		t.Fatalf("强制续期失败: %s", rep.Error)
	}
	newB, _ := store.Load(context.Background(), "a")
	if newB.Fingerprint == old.Fingerprint {
		t.Error("强制续期后指纹未变")
	}
	if prov.calls != 1 {
		t.Errorf("Ensure 调用次数 = %d", prov.calls)
	}
}

func TestForceRenewUnknown(t *testing.T) {
	cfg := testConfig(t)
	s := newTestScheduler(t, cfg, nil)
	if _, err := s.ForceRenew(context.Background(), "nope"); err == nil {
		t.Fatal("未知条目应报错")
	}
}

func TestFailureSendsWebhook(t *testing.T) {
	cfg := testConfig(t)
	// 配置 webhook 告警
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cfg.Notify.Webhook = srv.URL

	prov := &mockProv{fail: true}
	s := New(cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	}, log.New(io.Discard, "", 0))
	s.deployFactory = func(c config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		return &mockDeployer{name: c.Type}, nil
	}
	reports := s.RunOnce(context.Background())
	if reports[0].OK {
		t.Fatal("失败场景不应 OK")
	}
}

func readMeta(t *testing.T, f *storage.FS, name string) *certmgr.Meta {
	t.Helper()
	b, err := f.Load(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatal("条目不存在")
	}
	return &b.Meta
}

// --- 通知测试 ---

// captureNotifier 记录收到的通知事件(便于断言开关过滤)。
type captureNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (c *captureNotifier) Notify(_ context.Context, e notify.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *captureNotifier) kinds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, e.Kind)
	}
	return out
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestNotificationSwitches(t *testing.T) {
	cfg := testConfig(t)
	// 快到期 → 触发 renew(走签发+部署成功)
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	if err := store.Save(context.Background(), testBundle("a", []string{"a.com"}, time.Now().Add(5*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	prov := &mockProv{}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	cap := &captureNotifier{}
	s.notifiers = append(s.notifiers, cap)

	// 默认开关全关:不发送 success/expiring
	s.RunOnce(context.Background())
	if got := cap.kinds(); len(got) != 0 {
		t.Fatalf("默认不应发送成功/到期通知,实际: %v", got)
	}

	// 开启开关后重新扫描(mockProv 每次签发新证书,再放一张快到期证书触发 renew)
	store2, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	if err := store2.Save(context.Background(), testBundle("a", []string{"a.com"}, time.Now().Add(5*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	cfg.Notify.Events.Success = true
	cfg.Notify.Events.Expiring = true
	s2 := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	cap2 := &captureNotifier{}
	s2.notifiers = append(s2.notifiers, cap2)
	s2.RunOnce(context.Background())
	kinds := cap2.kinds()
	if !containsStr(kinds, "expiring") || !containsStr(kinds, "success") {
		t.Fatalf("开启后应发送 expiring 与 success,实际: %v", kinds)
	}
}

func TestExpiringNotifiedOncePerDay(t *testing.T) {
	cfg := testConfig(t)
	cfg.Notify.Events.Expiring = true
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	if err := store.Save(context.Background(), testBundle("a", []string{"a.com"}, time.Now().Add(5*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	// 每次签发都返回快到期证书 → 每轮仍触发 renew
	short := &mockProv{}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return short, nil
	})
	cap := &captureNotifier{}
	s.notifiers = append(s.notifiers, cap)

	s.RunOnce(context.Background())
	if !containsStr(cap.kinds(), "expiring") {
		t.Fatal("第一轮应发送到期提醒")
	}
	s.RunOnce(context.Background())
	n := 0
	for _, k := range cap.kinds() {
		if k == "expiring" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("24h 内到期提醒应只发一次,实际 %d 次: %v", n, cap.kinds())
	}
}

// --- 部署集成测试 ---

// mockDeployer 是测试用部署目标。
type mockDeployer struct {
	name string
	fail bool
}

func (m *mockDeployer) Name() string { return m.name }

func (m *mockDeployer) Deploy(context.Context, *certmgr.Bundle) error {
	if m.fail {
		return errors.New("mock 部署失败")
	}
	return nil
}

func TestIssueAutoDeploy(t *testing.T) {
	cfg := testConfig(t)
	prov := &mockProv{}
	dpl := &mockDeployer{name: "nginx"}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	s.deployFactory = func(cfg2 config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		return dpl, nil
	}

	reports := s.RunOnce(context.Background())
	if !reports[0].OK {
		t.Fatalf("签发+部署失败: %s", reports[0].Error)
	}
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	meta := readMeta(t, store, "a")
	if meta.DeployedFingerprint == "" {
		t.Error("部署后 DeployedFingerprint 未更新")
	}
	if len(meta.DeployedTargets) != 1 || meta.DeployedTargets[0] != "nginx" {
		t.Errorf("DeployedTargets = %v", meta.DeployedTargets)
	}
}

func TestDeployFailure(t *testing.T) {
	cfg := testConfig(t)
	prov := &mockProv{}
	dpl := &mockDeployer{name: "nginx", fail: true}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	s.deployFactory = func(cfg2 config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		return dpl, nil
	}

	reports := s.RunOnce(context.Background())
	if reports[0].OK {
		t.Fatal("部署失败时报告不应 OK")
	}
	if reports[0].Action != certmgr.ActionIssue {
		t.Errorf("动作 = %s", reports[0].Action)
	}
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	meta := readMeta(t, store, "a")
	if meta.DeployedFingerprint != "" {
		t.Error("部署失败后 DeployedFingerprint 不应更新")
	}
}

func TestDeployOnlyOnNextRun(t *testing.T) {
	cfg := testConfig(t)
	prov := &mockProv{}
	dpl := &mockDeployer{name: "nginx"}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	s.deployFactory = func(cfg2 config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		return dpl, nil
	}
	s.RunOnce(context.Background()) // 签发+部署成功

	// 再跑:证书有效且已部署 → skip,不再调用签发
	prov2 := &mockProv{}
	s2 := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov2, nil
	})
	s2.deployFactory = func(cfg2 config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		return dpl, nil
	}
	reports := s2.RunOnce(context.Background())
	if reports[0].Action != certmgr.ActionSkip {
		t.Fatalf("已部署应 skip,实际 %s(%s)", reports[0].Action, reports[0].Error)
	}
	if prov2.calls != 0 {
		t.Errorf("已部署后不应重新签发,calls = %d", prov2.calls)
	}
}

func TestDeployRetryAfterFailure(t *testing.T) {
	cfg := testConfig(t)
	prov := &mockProv{}
	// 先部署失败
	failDpl := &mockDeployer{name: "nginx", fail: true}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})
	s.deployFactory = func(cfg2 config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		return failDpl, nil
	}
	s.RunOnce(context.Background())

	// 下次:证书有效但未部署 → deploy(不重新签发)
	okDpl := &mockDeployer{name: "nginx"}
	prov2 := &mockProv{}
	s2 := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov2, nil
	})
	s2.deployFactory = func(cfg2 config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		return okDpl, nil
	}
	reports := s2.RunOnce(context.Background())
	if reports[0].Action != certmgr.ActionDeploy {
		t.Fatalf("补部署应 ActionDeploy,实际 %s(%s)", reports[0].Action, reports[0].Error)
	}
	if !reports[0].OK {
		t.Fatalf("补部署失败: %s", reports[0].Error)
	}
	if prov2.calls != 0 {
		t.Errorf("补部署不应重新签发,calls = %d", prov2.calls)
	}
}

func TestCheckOnlyNoMutation(t *testing.T) {
	cfg := testConfig(t)
	prov := &mockProv{}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return prov, nil
	})

	// 条目 a 从未签发:CheckOnly 应报告 issue(待签发),但绝不调用签发
	reports := s.CheckOnly(context.Background())
	if len(reports) != 1 {
		t.Fatalf("报告数 = %d,期望 1", len(reports))
	}
	if reports[0].Action != certmgr.ActionIssue {
		t.Errorf("未签发条目 CheckOnly 应 ActionIssue,实际 %s", reports[0].Action)
	}
	if !reports[0].OK {
		t.Errorf("纯检查不应报错: %s", reports[0].Error)
	}
	if prov.calls != 0 {
		t.Errorf("CheckOnly 不应调用签发,calls = %d", prov.calls)
	}

	// 存储目录不应出现任何产物(未执行签发/部署)
	store, err := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Load(context.Background(), "a")
	if err != nil {
		t.Fatalf("Load 应成功(空目录返回 nil,无错误): %v", err)
	}
	if b != nil {
		t.Error("CheckOnly 不应写证书产物")
	}
}

// TestCheckOnlyDeployBranchNoMutation 覆盖「证书有效但未部署」分支:
// CheckOnly 应报告 ActionDeploy(提示),但绝不实际部署、不更新 meta。
func TestCheckOnlyDeployBranchNoMutation(t *testing.T) {
	cfg := testConfig(t)
	store, _ := storage.NewFS(cfg.Certificates[0].Storage.Dir)
	// 已签发但未部署的有效证书
	bundle := testBundle("a", []string{"a.com"}, time.Now().Add(60*24*time.Hour))
	if err := store.Save(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}

	deployCalls := 0
	dpl := &mockDeployer{name: "nginx"}
	s := newTestScheduler(t, cfg, func(*config.CertificateConfig) (acme.Provisioner, error) {
		return &mockProv{}, nil
	})
	s.deployFactory = func(cfg2 config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
		deployCalls++
		return dpl, nil
	}

	reports := s.CheckOnly(context.Background())
	if reports[0].Action != certmgr.ActionDeploy {
		t.Fatalf("未部署证书 CheckOnly 应报告 ActionDeploy,实际 %s(%s)", reports[0].Action, reports[0].Error)
	}
	if !reports[0].OK {
		t.Fatalf("纯检查不应报错: %s", reports[0].Error)
	}
	if deployCalls != 0 {
		t.Errorf("CheckOnly 不应调用部署,deployFactory 被调 %d 次", deployCalls)
	}
	// meta 不应被改动:DeployedFingerprint 仍为空
	meta := readMeta(t, store, "a")
	if meta.DeployedFingerprint != "" || len(meta.DeployedTargets) != 0 {
		t.Errorf("CheckOnly 不应写部署 meta: %+v", meta)
	}
}