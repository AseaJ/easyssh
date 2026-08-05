package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/asea/easyssh/internal/config"
)

func TestDurationMarshalYAML(t *testing.T) {
	var d config.Duration
	if err := yaml.Unmarshal([]byte(`"6h"`), &d); err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "6h") {
		t.Errorf("Marshal 结果异常: %s", out)
	}
}

func TestGetConfigRoundTrip(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	view, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if view.CAServer == "" || len(view.Certificates) != 1 {
		t.Fatalf("视图异常: %+v", view)
	}
	c := view.Certificates[0]
	if c.Name != "example" || c.Challenge != "http-01" {
		t.Errorf("条目视图异常: %+v", c)
	}
	if len(c.Deploys) != 1 || c.Deploys[0].Type != "nginx" {
		t.Errorf("部署视图异常: %+v", c.Deploys)
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	view, _ := a.GetConfig()

	// 修改域名并新增调度值
	view.Certificates[0].Domains = []string{"noslop.top", "www.noslop.top"}
	view.RetryBackoff = []string{"1h", "6h"}
	msg, err := a.SaveConfig(view)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if !strings.Contains(msg, "已保存") {
		t.Errorf("返回消息异常: %s", msg)
	}
	// 文件已更新
	raw, _ := os.ReadFile(filepath.Join(dir, "easyssh.yaml"))
	if !strings.Contains(string(raw), "www.noslop.top") {
		t.Error("配置文件中未出现新域名")
	}
	// 重载后生效
	v2, _ := a.GetConfig()
	if v2.Certificates[0].Domains[1] != "www.noslop.top" {
		t.Errorf("重载后域名未生效: %v", v2.Certificates[0].Domains)
	}
}

func TestSaveConfigSecretToEnv(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	view, _ := a.GetConfig()

	// 模拟用户输入明文密钥
	view.Certificates[0].Challenge = "dns-01"
	view.Certificates[0].DNSProvider = "dnspod"
	view.Certificates[0].DNSOpts = map[string]string{
		"DNSPOD_API_KEY": "123456,secrettoken",
	}
	// 注入 mock persistEnv,避免真跑 setx;同时模拟真实写环境变量使 reload 可展开
	var persisted []string
	persistEnvFn = func(name, value string) error {
		persisted = append(persisted, name+"="+value)
		os.Setenv(name, value)
		return nil
	}
	defer func() {
		persistEnvFn = persistEnv
		os.Unsetenv("GOZS_EXAMPLE_DNSPOD_API_KEY")
	}()

	msg, err := a.SaveConfig(view)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("persistEnv 调用数 = %d,期望 1", len(persisted))
	}
	if !strings.Contains(persisted[0], "123456") {
		t.Errorf("持久化值异常: %v", persisted)
	}
	// 配置文件里应只存引用,无明文
	raw, _ := os.ReadFile(filepath.Join(dir, "easyssh.yaml"))
	if strings.Contains(string(raw), "secrettoken") {
		t.Error("配置文件不应出现明文密钥")
	}
	if !strings.Contains(string(raw), "{{env:GOZS_EXAMPLE_DNSPOD_API_KEY}}") {
		t.Errorf("应保存为 env 引用,实际: %s", raw)
	}
	if !strings.Contains(msg, "GOZS_EXAMPLE_DNSPOD_API_KEY") {
		t.Errorf("返回消息应提示环境变量名: %s", msg)
	}
}

func TestEnvName(t *testing.T) {
	cases := []struct{ entry, key, want string }{
		{"example", "DNSPOD_API_KEY", "GOZS_EXAMPLE_DNSPOD_API_KEY"},
		{"my-site", "api_token", "GOZS_MY_SITE_API_TOKEN"},
	}
	for _, c := range cases {
		if got := envName(c.entry, c.key); got != c.want {
			t.Errorf("envName(%s,%s) = %s,期望 %s", c.entry, c.key, got, c.want)
		}
	}
}

func TestBuildConfigInvalid(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	view, _ := a.GetConfig()
	view.Certificates[0].Domains = []string{"bad domain.com"}
	if _, err := a.SaveConfig(view); err == nil {
		t.Fatal("非法域名应保存失败")
	}
}

func TestSaveConfigHostsAndSMTP(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	view, _ := a.GetConfig()

	// 新增 SSH 主机 + SMTP 邮件配置
	view.Hosts = []HostEditView{
		{Name: "prod", Host: "10.0.0.1", Port: 22, User: "deploy", Key: "/k", RemotePath: "/etc/ssl", ReloadCmd: "nginx -s reload"},
	}
	view.SMTPHost = "smtp.qq.com"
	view.SMTPPort = 465
	view.SMTPUser = "me@qq.com"
	view.SMTPPass = "authcode123"
	view.SMTPTo = []string{"ops@example.com", "admin@example.com"}

	var persisted []string
	persistEnvFn = func(name, value string) error {
		persisted = append(persisted, name)
		os.Setenv(name, value)
		return nil
	}
	defer func() {
		persistEnvFn = persistEnv
		os.Unsetenv("GOZS_SMTP_PASS")
	}()

	msg, err := a.SaveConfig(view)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if !strings.Contains(msg, "GOZS_SMTP_PASS") {
		t.Errorf("应提示 SMTP 环境变量: %s", msg)
	}
	// 配置文件含 hosts 与 smtp 引用,不含明文授权码
	raw, _ := os.ReadFile(filepath.Join(dir, "easyssh.yaml"))
	if !strings.Contains(string(raw), "prod") || !strings.Contains(string(raw), "smtp.qq.com") {
		t.Error("配置文件中缺少 hosts/smtp 配置")
	}
	if strings.Contains(string(raw), "authcode123") {
		t.Error("配置文件不应出现明文授权码")
	}
	if !strings.Contains(string(raw), "{{env:GOZS_SMTP_PASS}}") {
		t.Error("应保存为 env 引用")
	}
	// 重载后 hosts 可用(校验 host_ref 引用存在)
	view2, _ := a.GetConfig()
	if len(view2.Hosts) != 1 || view2.Hosts[0].Name != "prod" {
		t.Errorf("重载后 hosts 异常: %+v", view2.Hosts)
	}
}

func TestSaveConfigHostRefValidation(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	view, _ := a.GetConfig()
	// 引用不存在的 host → 保存应失败
	view.Certificates[0].Deploys = []DeployEditView{{Type: "ssh", HostRef: "nope"}}
	if _, err := a.SaveConfig(view); err == nil {
		t.Fatal("引用不存在的 host 应保存失败")
	}
}

func TestSaveConfigDNSNonSecretOptsStayPlain(t *testing.T) {
	dir := writeTestConfig(t)
	a := NewApp(dir)
	a.Startup(context.Background())
	view, _ := a.GetConfig()

	view.Certificates[0].Challenge = "dns-01"
	view.Certificates[0].DNSProvider = "dnspod"
	view.Certificates[0].DNSOpts = map[string]string{
		"DNSPOD_API_KEY":             "123456,secrettoken",
		"DNSPOD_POLLING_INTERVAL":    "30",
		"DNSPOD_PROPAGATION_TIMEOUT": "120",
	}
	var persisted []string
	persistEnvFn = func(name, value string) error {
		persisted = append(persisted, name)
		os.Setenv(name, value)
		return nil
	}
	defer func() {
		persistEnvFn = persistEnv
		os.Unsetenv("GOZS_EXAMPLE_DNSPOD_API_KEY")
	}()

	if _, err := a.SaveConfig(view); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	// 只有敏感密钥转 env;非敏感参数明文落盘
	if len(persisted) != 1 || persisted[0] != "GOZS_EXAMPLE_DNSPOD_API_KEY" {
		t.Errorf("persistEnv 调用异常: %v", persisted)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "easyssh.yaml"))
	s := string(raw)
	if strings.Contains(s, "secrettoken") {
		t.Error("配置文件不应出现明文密钥")
	}
	if !strings.Contains(s, "{{env:GOZS_EXAMPLE_DNSPOD_API_KEY}}") {
		t.Error("敏感密钥应保存为 env 引用")
	}
	if !strings.Contains(s, "DNSPOD_POLLING_INTERVAL: \"30\"") && !strings.Contains(s, "DNSPOD_POLLING_INTERVAL: 30") {
		t.Errorf("非敏感参数应明文保存,实际: %s", s)
	}
	// 重载后 GetConfig 应原样带回非敏感参数(明文),敏感密钥仍是引用
	v2, _ := a.GetConfig()
	opts := v2.Certificates[0].DNSOpts
	if opts["DNSPOD_POLLING_INTERVAL"] != "30" {
		t.Errorf("非敏感参数应明文返回,实际: %q", opts["DNSPOD_POLLING_INTERVAL"])
	}
	if opts["DNSPOD_API_KEY"] != "{{env:GOZS_EXAMPLE_DNSPOD_API_KEY}}" {
		t.Errorf("敏感密钥应返回引用,实际: %q", opts["DNSPOD_API_KEY"])
	}
}

func TestGetConfigExpandsNonSecretEnvRef(t *testing.T) {
	dir := writeTestConfig(t)
	// 手工构造:非敏感参数被存成 env 引用(模拟旧版本保存)
	rawPath := filepath.Join(dir, "easyssh.yaml")
	raw, _ := os.ReadFile(rawPath)
	patched := strings.Replace(string(raw),
		"    challenge: http-01",
		"    challenge: dns-01\n    dns_provider: dnspod\n    dns_provider_opts:\n      DNSPOD_POLLING_INTERVAL: \"{{env:GOZS_OLD_POLL}}\"\n      DNSPOD_API_KEY: \"{{env:GOZS_OLD_KEY}}\"", 1)
	if err := os.WriteFile(rawPath, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Setenv("GOZS_OLD_POLL", "45")
	defer os.Unsetenv("GOZS_OLD_POLL")

	a := NewApp(dir)
	a.Startup(context.Background())
	view, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	opts := view.Certificates[0].DNSOpts
	if opts["DNSPOD_POLLING_INTERVAL"] != "45" {
		t.Errorf("非敏感参数的 env 引用应展开为明文,实际: %q", opts["DNSPOD_POLLING_INTERVAL"])
	}
	if opts["DNSPOD_API_KEY"] != "{{env:GOZS_OLD_KEY}}" {
		t.Errorf("敏感密钥应保持引用形式,实际: %q", opts["DNSPOD_API_KEY"])
	}
}
