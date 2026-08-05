package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHostCertFilename 验证 hosts 的 cert_filename/key_filename 校验:非法值(路径穿越)被拒绝,合法值通过。
func TestHostCertFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")

	write := func(t *testing.T, body string) *Config {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}

	// 合法:自定义文件名
	cfg := write(t, `
ca: {server: https://acme.example/directory, account_key: ./k}
hosts:
  - {name: h1, host: 1.2.3.4, user: root, cert_filename: aijijie.com_bundle.crt, key_filename: aijijie.com.key}
certificates:
  - name: a
    domains: [a.com]
    challenge: dns-01
    dns_provider: dnspod
    storage: {dir: ./certs/a}
`)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法文件名不应报错: %v", err)
	}
	if cfg.Hosts[0].CertFilename != "aijijie.com_bundle.crt" || cfg.Hosts[0].KeyFilename != "aijijie.com.key" {
		t.Fatalf("字段未正确解析: %+v", cfg.Hosts[0])
	}

	// 非法:路径穿越(Load 内含校验,直接断言报错)
	bad := []string{"../evil.pem", "a/b.pem", "..", "x\\y.pem", "a:b"}
	for _, fn := range bad {
		body := `
ca: {server: https://acme.example/directory, account_key: ./k}
hosts:
  - {name: h1, host: 1.2.3.4, user: root, cert_filename: ` + fn + `}
certificates:
  - name: a
    domains: [a.com]
    challenge: dns-01
    dns_provider: dnspod
    storage: {dir: ./certs/a}
`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("非法文件名 %q 应被拒绝", fn)
		}
	}
}
