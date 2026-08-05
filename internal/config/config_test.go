package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolvePaths(t *testing.T) {
	base := `C:\proj`
	cfg := &Config{
		CA: CAConfig{AccountKey: "./data/account.key"},
		Hosts: []HostConfig{
			{Name: "h", Key: "C:/abs/key.pem"},
			{Name: "h2", Key: "./ssh/id_ed25519"},
		},
		Certificates: []CertificateConfig{
			{
				Name:    "c",
				Domains: []string{"a.com"},
				Challenge: "dns-01",
				DNSProvider: "dnspod",
				Storage: StorageConfig{Dir: "./certs/a"},
				Deploy: []DeployConfig{
					{Type: "ssh", Key: "./keys/deploy.key", RemotePath: "/etc/nginx/ssl"},
					{Type: "nginx", CertPath: "./out/fullchain.pem", KeyPath: "./out/privkey.pem"},
					{Type: "file", Dir: "./out"},
				},
			},
		},
	}
	ResolvePaths(cfg, base)

	want := func(got, want, field string) {
		if got != want {
			t.Errorf("%s = %q,期望 %q", field, got, want)
		}
	}
	want(cfg.CA.AccountKey, `C:\proj\data\account.key`, "ca.account_key")
	want(cfg.Hosts[0].Key, `C:\abs\key.pem`, "hosts[0].key(绝对路径保持)")
	want(cfg.Hosts[1].Key, `C:\proj\ssh\id_ed25519`, "hosts[1].key")
	want(cfg.Certificates[0].Storage.Dir, `C:\proj\certs\a`, "storage.dir")
	want(cfg.Certificates[0].Deploy[0].Key, `C:\proj\keys\deploy.key`, "deploy.ssh.key")
	// 远程路径不得被解析
	want(cfg.Certificates[0].Deploy[0].RemotePath, "/etc/nginx/ssl", "deploy.ssh.remote_path")
	want(cfg.Certificates[0].Deploy[1].CertPath, `C:\proj\out\fullchain.pem`, "deploy.nginx.cert_path")
	want(cfg.Certificates[0].Deploy[2].Dir, `C:\proj\out`, "deploy.file.dir")
}

func TestCloneIsIndependent(t *testing.T) {
	if Clone(nil) != nil {
		t.Fatal("Clone(nil) 应返回 nil")
	}
	cfg := &Config{
		CA: CAConfig{AccountKey: "./data/account.key"},
		Certificates: []CertificateConfig{
			{Name: "c", Domains: []string{"a.com"}, Challenge: "http-01", Storage: StorageConfig{Dir: "./certs/a"}},
		},
	}
	c2 := Clone(cfg)
	c2.CA.AccountKey = "changed"
	c2.Certificates[0].Storage.Dir = "changed"
	if cfg.CA.AccountKey != "./data/account.key" || cfg.Certificates[0].Storage.Dir != "./certs/a" {
		t.Error("修改 Clone 副本不应影响原配置")
	}
}

const validYAML = `
ca:
  server: https://acme-v02.api.letsencrypt.org/directory
  email: ops@example.com
  account_key: ./data/account.key
certificates:
  - name: example
    domains: [example.com, www.example.com]
    challenge: http-01
    storage:
      dir: ./certs/example
    deploy:
      - type: nginx
schedule:
  check_interval: 6h
  renew_before: 30d
  retry_backoff: [1h, 6h, 24h]
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go-zs.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("证书条目数 = %d,期望 1", len(cfg.Certificates))
	}
	if cfg.Schedule.CheckInterval.Std() != 6*time.Hour {
		t.Errorf("check_interval 解析错误: %v", cfg.Schedule.CheckInterval.Std())
	}
	if cfg.Certificates[0].Deploy[0].ReloadCmd != "nginx -s reload" {
		t.Errorf("nginx reload_cmd 默认值未填充: %q", cfg.Certificates[0].Deploy[0].ReloadCmd)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(writeTemp(t, `
certificates:
  - name: a
    domains: [a.example.com]
    challenge: http-01
    storage: {dir: ./certs/a}
`))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.CA.Server != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Errorf("默认 CA server 错误: %s", cfg.CA.Server)
	}
	if cfg.Schedule.RenewBefore.Std() != 30*24*time.Hour {
		t.Errorf("默认 renew_before 错误: %v", cfg.Schedule.RenewBefore.Std())
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"空条目", `certificates: []`, "至少需要一个"},
		{"无域名", `
certificates:
  - name: a
    domains: []
    challenge: http-01
    storage: {dir: ./certs/a}`, "domains 至少需要一个"},
		{"非法挑战", `
certificates:
  - name: a
    domains: [a.com]
    challenge: tls-01
    storage: {dir: ./certs/a}`, "challenge 必须是"},
		{"通配符用 http-01", `
certificates:
  - name: a
    domains: ["*.example.com"]
    challenge: http-01
    storage: {dir: ./certs/a}`, "通配符域名时 challenge 必须为 dns-01"},
		{"dns-01 缺 provider", `
certificates:
  - name: a
    domains: [a.com]
    challenge: dns-01
    storage: {dir: ./certs/a}`, "必须配置 dns_provider"},
		{"同名条目", `
certificates:
  - {name: a, domains: [a.com], challenge: http-01, storage: {dir: ./certs/a}}
  - {name: a, domains: [b.com], challenge: http-01, storage: {dir: ./certs/b}}`, "条目名重复"},
		{"目录冲突", `
certificates:
  - {name: a, domains: [a.com], challenge: http-01, storage: {dir: ./certs/x}}
  - {name: b, domains: [b.com], challenge: http-01, storage: {dir: ./certs/x}}`, "同一存储目录"},
		{"ssh 缺参数", `
certificates:
  - name: a
    domains: [a.com]
    challenge: http-01
    storage: {dir: ./certs/a}
    deploy:
      - {type: ssh, host: 1.2.3.4}`, "host_ref"},
		{"未知 deploy 类型", `
certificates:
  - name: a
    domains: [a.com]
    challenge: http-01
    storage: {dir: ./certs/a}
    deploy:
      - {type: docker}`, "未知 deploy 类型"},
		{"非法域名", `
certificates:
  - name: a
    domains: ["bad domain.com"]
    challenge: http-01
    storage: {dir: ./certs/a}`, "域名"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeTemp(t, tc.yaml))
			if err == nil {
				t.Fatalf("期望错误包含 %q,实际无错误", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("错误 %q 未包含期望片段 %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSMTPToYAMLCompat(t *testing.T) {
	const base = `
ca:
  server: https://acme-v02.api.letsencrypt.org/directory
certificates:
  - {name: a, domains: [a.com], challenge: http-01, storage: {dir: ./certs/a}}
`
	// 单字符串收件人(旧格式)
	cfg, err := Load(writeTemp(t, base+`
notify:
  smtp:
    host: smtp.qq.com
    port: 465
    user: me@qq.com
    pass: secret
    to: ops@example.com
`))
	if err != nil {
		t.Fatalf("单字符串收件人解析失败: %v", err)
	}
	if len(cfg.Notify.SMTP.To) != 1 || cfg.Notify.SMTP.To[0] != "ops@example.com" {
		t.Errorf("单字符串收件人解析错误: %v", cfg.Notify.SMTP.To)
	}

	// 多收件人列表
	cfg2, err := Load(writeTemp(t, base+`
notify:
  smtp:
    host: smtp.qq.com
    user: me@qq.com
    pass: secret
    to: [ops@example.com, admin@example.com]
  events:
    expiring: true
    success: true
`))
	if err != nil {
		t.Fatalf("多收件人解析失败: %v", err)
	}
	if len(cfg2.Notify.SMTP.To) != 2 || cfg2.Notify.SMTP.To[1] != "admin@example.com" {
		t.Errorf("多收件人解析错误: %v", cfg2.Notify.SMTP.To)
	}
	if !cfg2.Notify.Events.Expiring || !cfg2.Notify.Events.Success {
		t.Errorf("事件开关解析错误: %+v", cfg2.Notify.Events)
	}
}

func TestExpandEnvRefs(t *testing.T) {
	t.Setenv("CF_TOKEN", "secret-token")
	yaml := `
certificates:
  - name: a
    domains: [a.com]
    challenge: dns-01
    dns_provider: cloudflare
    dns_provider_opts:
      api_token: "{{env:CF_TOKEN}}"
    storage: {dir: ./certs/a}
`
	cfg, err := Load(writeTemp(t, yaml))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if err := ExpandEnvRefs(cfg); err != nil {
		t.Fatalf("ExpandEnvRefs 失败: %v", err)
	}
	got := cfg.Certificates[0].DNSProviderOpts["api_token"]
	if got != "secret-token" {
		t.Errorf("env 展开错误: %q", got)
	}
}

func TestExpandEnvRefsMissing(t *testing.T) {
	yaml := `
certificates:
  - name: a
    domains: [a.com]
    challenge: dns-01
    dns_provider: cloudflare
    dns_provider_opts:
      api_token: "{{env:NOT_SET_VAR_XYZ}}"
    storage: {dir: ./certs/a}
`
	cfg, err := Load(writeTemp(t, yaml))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if err := ExpandEnvRefs(cfg); err == nil {
		t.Fatal("期望缺失环境变量报错,实际无错误")
	}
}

func TestLoadAndExpandLenient(t *testing.T) {
	t.Setenv("SET_VAR", "ok")
	yaml := `
certificates:
  - name: a
    domains: [a.com]
    challenge: dns-01
    dns_provider: cloudflare
    dns_provider_opts:
      api_token: "{{env:SET_VAR}}"
      other: "{{env:MISSING_XYZ}}"
    storage: {dir: ./certs/a}
`
	cfg, missing, err := LoadAndExpandLenient(writeTemp(t, yaml))
	if err != nil {
		t.Fatalf("宽容加载失败: %v", err)
	}
	// 已设置的 env 被展开,缺失的保留引用并列入 missing
	if cfg.Certificates[0].DNSProviderOpts["api_token"] != "ok" {
		t.Errorf("已设置 env 未展开: %v", cfg.Certificates[0].DNSProviderOpts)
	}
	if cfg.Certificates[0].DNSProviderOpts["other"] != "{{env:MISSING_XYZ}}" {
		t.Errorf("缺失 env 应保留引用: %v", cfg.Certificates[0].DNSProviderOpts)
	}
	if len(missing) != 1 || missing[0] != "MISSING_XYZ" {
		t.Errorf("missing 列表异常: %v", missing)
	}
}
