package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asea/easyssh/internal/config"
)

// --- 测试工具 ---

// testEnv 构造一个临时目录场景:配置 + 证书产物 + SSH 私钥。
type testEnv struct {
	dir       string
	configPath string
	cfg       *config.Config
	sshKey    string
	acctKey   string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	// 证书产物目录
	certDir := filepath.Join(dir, "certs", "example")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// 假证书产物
	fullchain := []byte("-----BEGIN CERTIFICATE-----\nFAKE-FULLCHAIN\n-----END CERTIFICATE-----\n")
	privkey := []byte("-----BEGIN PRIVATE KEY-----\nFAKE-PRIVKEY\n-----END PRIVATE KEY-----\n")
	meta := []byte(`{"name":"example","domains":["example.com"]}`)
	for name, data := range map[string][]byte{
		"fullchain.pem": fullchain, "privkey.pem": privkey, "meta.json": meta,
	} {
		if err := os.WriteFile(filepath.Join(certDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// SSH 私钥
	sshKey := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(sshKey, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nFAKE-SSH\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// ACME 账号密钥
	acctKey := filepath.Join(dir, "data", "account.key")
	if err := os.MkdirAll(filepath.Dir(acctKey), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(acctKey, []byte("-----BEGIN EC PRIVATE KEY-----\nFAKE-ACCT\n-----END EC PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		CA: config.CAConfig{Server: "https://acme.example.com/directory", Email: "ops@example.com", AccountKey: "./data/account.key"},
		Hosts: []config.HostConfig{{
			Name: "prod", Host: "203.0.113.10", Port: 22, User: "deploy", Key: "./id_ed25519", RemotePath: "/etc/ssl",
		}},
		Certificates: []config.CertificateConfig{{
			Name:       "example",
			Domains:    []string{"example.com"},
			Challenge:  "dns-01",
			DNSProvider: "dnspod",
			DNSProviderOpts: map[string]string{"api_token": "{{env:GOZS_TEST_TOKEN}}"},
			Storage:    config.StorageConfig{Dir: "./certs/example"},
			Deploy: []config.DeployConfig{{
				Type: "ssh", HostRef: "prod",
			}},
		}},
		Schedule: config.DefaultSchedule(),
	}
	configPath := filepath.Join(dir, "easyssh.yaml")
	data, err := yamlMarshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return &testEnv{dir: dir, configPath: configPath, cfg: cfg, sshKey: sshKey, acctKey: acctKey}
}

// exportAll 用完整范围导出一个包。
func (e *testEnv) exportAll(t *testing.T, pw string) string {
	t.Helper()
	out := filepath.Join(e.dir, "out.zsbundle")
	if _, err := Export(out, ExportOptions{
		ConfigPath: e.configPath,
		Scope:      Scope{Config: true, Secrets: true, Certs: true, SSHKeys: true},
		Password:   pw,
	}); err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	return out
}

// --- 加密单测 ---

func TestEncryptDecryptRoundTrip(t *testing.T) {
	kdf := DefaultKDF()
	plain := []byte("hello 证书 bundle")
	blob, err := encryptBlob("correct-horse-battery", plain, kdf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decryptBlob("correct-horse-battery", blob, kdf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Errorf("往返不一致: got %q want %q", got, plain)
	}
	// 两次加密 salt 不同 → 密文不同
	blob2, _ := encryptBlob("correct-horse-battery", plain, kdf)
	if string(blob) == string(blob2) {
		t.Error("同一明文两次加密应产生不同密文(salt 应随机)")
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	kdf := DefaultKDF()
	blob, err := encryptBlob("correct-horse-battery", []byte("secret"), kdf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptBlob("wrong-password-123", blob, kdf); err != ErrBadPassword {
		t.Errorf("错误口令应返回 ErrBadPassword, got %v", err)
	}
}

func TestDecryptTampered(t *testing.T) {
	kdf := DefaultKDF()
	blob, _ := encryptBlob("correct-horse-battery", []byte("secret"), kdf)
	blob[len(blob)-1] ^= 0xFF // 篡改密文末尾
	if _, err := decryptBlob("correct-horse-battery", blob, kdf); err != ErrBadPassword {
		t.Errorf("篡改密文应返回 ErrBadPassword, got %v", err)
	}
}

func TestPasswordStrength(t *testing.T) {
	for _, pw := range []string{"short", "1234567890", "1234567890123"} {
		if err := validatePassword(pw); err == nil {
			t.Errorf("口令 %q 应被拒绝", pw)
		}
	}
	if err := validatePassword("GoodPassw0rd!"); err != nil {
		t.Errorf("强口令应通过: %v", err)
	}
}

// --- 导出/导入 round-trip ---

func TestExportImportFullRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	// 设置环境变量(导出 secrets 需要)
	pw := "BundlePassw0rd!"
	os.Setenv("GOZS_TEST_TOKEN", "token-12345")
	defer os.Unsetenv("GOZS_TEST_TOKEN")

	bundlePath := e.exportAll(t, pw)

	// 读回验证
	b, err := ReadBundle(bundlePath, pw)
	if err != nil {
		t.Fatalf("读取导出包: %v", err)
	}
	if b.Manifest.FormatVersion != FormatVersion {
		t.Errorf("版本不符: %d", b.Manifest.FormatVersion)
	}
	if !b.Manifest.Scope.Certs || !b.Manifest.Scope.Secrets || !b.Manifest.Scope.SSHKeys {
		t.Errorf("scope 不符: %+v", b.Manifest.Scope)
	}
	// 证书产物
	if len(b.Certs) != 1 || b.Certs["example"]["fullchain.pem"] == nil {
		t.Error("证书产物未正确打包")
	}
	// 密钥
	if b.Secrets.Env["GOZS_TEST_TOKEN"] != "token-12345" {
		t.Error("env 密钥未正确导出")
	}
	if !strings.Contains(b.Secrets.AccountKey, "FAKE-ACCT") {
		t.Error("account.key 未正确导出")
	}
	// SSH 私钥
	if !strings.Contains(b.SSHKeys.Keys["./id_ed25519"], "FAKE-SSH") {
		t.Error("SSH 私钥未正确导出")
	}
	// 配置保持 {{env:VAR}} 引用(未展开)
	if !strings.Contains(string(b.Config), "{{env:GOZS_TEST_TOKEN}}") {
		t.Error("config.yaml 应保留 env 引用,不展开")
	}
}

func TestExportWithoutSecretsNoPassword(t *testing.T) {
	e := newTestEnv(t)
	out := filepath.Join(e.dir, "cfg-only.zsbundle")
	if _, err := Export(out, ExportOptions{
		ConfigPath: e.configPath,
		Scope:      Scope{Config: true},
	}); err != nil {
		t.Fatalf("仅配置导出不应需要口令: %v", err)
	}
	// 无口令读取(档1 无加密段,不应要求口令)
	if _, err := ReadBundle(out, ""); err != nil {
		t.Fatalf("档1 无需口令即可读: %v", err)
	}
}

func TestExportSecretsRequiresPassword(t *testing.T) {
	e := newTestEnv(t)
	os.Setenv("GOZS_TEST_TOKEN", "token-12345")
	defer os.Unsetenv("GOZS_TEST_TOKEN")
	_, err := Export(filepath.Join(e.dir, "x.zsbundle"), ExportOptions{
		ConfigPath: e.configPath,
		Scope:      Scope{Config: true, Secrets: true},
		Password:   "weak", // 弱口令应被拒绝
	})
	if err == nil {
		t.Error("弱口令应被拒绝")
	}
}

func TestExportSecretsMissingEnv(t *testing.T) {
	e := newTestEnv(t)
	// 不设置 GOZS_TEST_TOKEN
	_, err := Export(filepath.Join(e.dir, "x.zsbundle"), ExportOptions{
		ConfigPath: e.configPath,
		Scope:      Scope{Config: true, Secrets: true},
		Password:   "GoodPassw0rd!",
	})
	if err == nil || !strings.Contains(err.Error(), "GOZS_TEST_TOKEN") {
		t.Errorf("缺失 env 应报错并指名, got %v", err)
	}
}

func TestImportNeedsPassword(t *testing.T) {
	e := newTestEnv(t)
	pw := "BundlePassw0rd!"
	os.Setenv("GOZS_TEST_TOKEN", "token-12345")
	defer os.Unsetenv("GOZS_TEST_TOKEN")
	bundlePath := e.exportAll(t, pw)
	if _, err := ReadBundle(bundlePath, ""); err != ErrNeedPassword {
		t.Errorf("含加密段无口令应返回 ErrNeedPassword, got %v", err)
	}
}

// --- 冲突与合并 ---

func TestApplyImportToEmptyTarget(t *testing.T) {
	e := newTestEnv(t)
	pw := "BundlePassw0rd!"
	os.Setenv("GOZS_TEST_TOKEN", "token-12345")
	defer os.Unsetenv("GOZS_TEST_TOKEN")
	bundlePath := e.exportAll(t, pw)
	b, err := ReadBundle(bundlePath, pw)
	if err != nil {
		t.Fatal(err)
	}
	// 全新目标目录
	target := t.TempDir()
	targetCfg := filepath.Join(target, "easyssh.yaml")
	persisted := map[string]string{}
	res, err := ApplyImport(b, MergeOptions{
		ConfigPath: targetCfg,
		Conflict:   ConflictAppend,
		TargetBase: target,
		PersistEnv: func(name, value string) error { persisted[name] = value; return nil },
	})
	if err != nil {
		t.Fatalf("导入: %v", err)
	}
	if len(res.ImportedCerts) != 1 || res.ImportedCerts[0] != "example" {
		t.Errorf("条目未导入: %+v", res.ImportedCerts)
	}
	if persisted["GOZS_TEST_TOKEN"] != "token-12345" {
		t.Error("env 密钥未写入")
	}
	// 配置落盘可读且校验通过
	cfg, err := config.Load(targetCfg)
	if err != nil {
		t.Fatalf("导入后配置不可读: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Error("导入后条目数错误")
	}
	// 证书产物落盘
	if _, err := os.Stat(filepath.Join(target, "certs", "example", "fullchain.pem")); err != nil {
		t.Error("证书产物未落盘")
	}
	// SSH 私钥落盘
	if _, err := os.Stat(filepath.Join(target, "id_ed25519")); err != nil {
		t.Error("SSH 私钥未落盘")
	}
	// account.key 落盘
	if _, err := os.Stat(filepath.Join(target, "data", "account.key")); err != nil {
		t.Error("account.key 未落盘")
	}
}

func TestApplyImportConflictAppend(t *testing.T) {
	e := newTestEnv(t)
	pw := "BundlePassw0rd!"
	os.Setenv("GOZS_TEST_TOKEN", "token-12345")
	defer os.Unsetenv("GOZS_TEST_TOKEN")
	bundlePath := e.exportAll(t, pw)
	b, _ := ReadBundle(bundlePath, pw)

	target := t.TempDir()
	targetCfg := filepath.Join(target, "easyssh.yaml")
	// 目标已有一个同名条目与同名 host
	tcfg := &config.Config{
		CA:           config.CAConfig{Server: "https://acme.example.com/directory", Email: "ops@example.com", AccountKey: "./data/account.key"},
		Hosts:        []config.HostConfig{{Name: "prod", Host: "10.0.0.1", Port: 22, User: "u", Key: ""}},
		Certificates: []config.CertificateConfig{{Name: "example", Domains: []string{"old.com"}, Challenge: "http-01", Storage: config.StorageConfig{Dir: "./certs/old"}}},
		Schedule:     config.DefaultSchedule(),
	}
	cfgYAML, _ := yamlMarshal(tcfg)
	os.WriteFile(targetCfg, cfgYAML, 0o644)

	_, err := ApplyImport(b, MergeOptions{
		ConfigPath: targetCfg,
		Conflict:   ConflictAppend,
		TargetBase: target,
		PersistEnv: func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("导入: %v", err)
	}
	// 证书:原有 example + 新 example-2
	got, _ := config.Load(targetCfg)
	if len(got.Certificates) != 2 {
		t.Fatalf("append 后应有 2 个条目: %+v", got.Certificates)
	}
	names := []string{got.Certificates[0].Name, got.Certificates[1].Name}
	if !(names[0] == "example" && names[1] == "example-2" || names[0] == "example-2" && names[1] == "example") {
		t.Errorf("新条目应改名为 example-2: %v", names)
	}
	// hosts:prod 冲突 → prod-2,且新条目的 host_ref 应联动改为 prod-2
	if len(got.Hosts) != 2 || got.Hosts[1].Name != "prod-2" {
		t.Errorf("hosts 应改名 prod-2: %+v", got.Hosts)
	}
	// 新条目 example-2 的 deploy.host_ref 应为 prod-2
	var refs []string
	for _, c := range got.Certificates {
		for _, d := range c.Deploy {
			refs = append(refs, d.HostRef)
		}
	}
	found := false
	for _, r := range refs {
		if r == "prod-2" {
			found = true
		}
	}
	if !found {
		t.Errorf("host_ref 应联动改为 prod-2, got %v", refs)
	}
}

func TestApplyImportConflictSkip(t *testing.T) {
	e := newTestEnv(t)
	pw := "BundlePassw0rd!"
	os.Setenv("GOZS_TEST_TOKEN", "token-12345")
	defer os.Unsetenv("GOZS_TEST_TOKEN")
	bundlePath := e.exportAll(t, pw)
	b, _ := ReadBundle(bundlePath, pw)

	target := t.TempDir()
	targetCfg := filepath.Join(target, "easyssh.yaml")
	tcfg := &config.Config{
		CA:           config.CAConfig{Server: "https://acme.example.com/directory", Email: "ops@example.com", AccountKey: "./data/account.key"},
		Hosts:        []config.HostConfig{{Name: "prod", Host: "10.0.0.1", Port: 22, User: "u"}},
		Certificates: []config.CertificateConfig{{Name: "example", Domains: []string{"old.com"}, Challenge: "http-01", Storage: config.StorageConfig{Dir: "./certs/old"}}},
		Schedule:     config.DefaultSchedule(),
	}
	cfgYAML, _ := yamlMarshal(tcfg)
	os.WriteFile(targetCfg, cfgYAML, 0o644)

	res, err := ApplyImport(b, MergeOptions{
		ConfigPath: targetCfg,
		Conflict:   ConflictSkip,
		TargetBase: target,
		PersistEnv: func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("导入: %v", err)
	}
	got, _ := config.Load(targetCfg)
	if len(got.Certificates) != 1 {
		t.Errorf("skip 模式不应新增条目: %d", len(got.Certificates))
	}
	if len(res.Skipped) == 0 {
		t.Error("skip 模式应记录跳过项")
	}
}

// --- 预览与边界 ---
func TestPreview(t *testing.T) {
	e := newTestEnv(t)
	pw := "BundlePassw0rd!"
	os.Setenv("GOZS_TEST_TOKEN", "token-12345")
	defer os.Unsetenv("GOZS_TEST_TOKEN")
	bundlePath := e.exportAll(t, pw)
	b, _ := ReadBundle(bundlePath, pw)
	p := b.Preview()
	if len(p.CertNames) != 1 || p.CertNames[0] != "example" {
		t.Errorf("预览条目不符: %v", p.CertNames)
	}
	if !p.HasSecrets || !p.HasCerts || !p.HasSSHKeys {
		t.Error("预览范围标志不符")
	}
}

func TestRejectWrongFormat(t *testing.T) {
	// 非 zip 文件
	bad := filepath.Join(t.TempDir(), "bad.zsbundle")
	os.WriteFile(bad, []byte("not a zip"), 0o644)
	if _, err := ReadBundle(bad, ""); err == nil {
		t.Error("非 zip 包应被拒绝")
	}
}

func TestParseConflict(t *testing.T) {
	for _, s := range []string{"append", "skip", "overwrite"} {
		if _, err := ParseConflict(s); err != nil {
			t.Errorf("%q 应可解析: %v", s, err)
		}
	}
	if _, err := ParseConflict("bogus"); err == nil {
		t.Error("非法策略应报错")
	}
}
