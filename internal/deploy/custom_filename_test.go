package deploy

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSSHDeployCustomFilename 验证自定义证书/私钥文件名:部署到指定文件名而非默认名。
func TestSSHDeployCustomFilename(t *testing.T) {
	srv, keyPath := startSSHServer(t)
	host, port, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	var p int
	fmt.Sscanf(port, "%d", &p)
	d, err := NewSSH(SSHConfig{
		Host:         host,
		Port:         p,
		User:         "test",
		Key:          keyPath,
		RemotePath:   srv.dir,
		ReloadCmd:    "nginx -s reload",
		CertFilename: "aijijie.com_bundle.crt",
		KeyFilename:  "aijijie.com.key",
		Timeout:      10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	b := sshBundle()
	if err := d.Deploy(context.Background(), b); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	// 自定义文件名已生成
	certData, err := os.ReadFile(filepath.Join(srv.dir, "aijijie.com_bundle.crt"))
	if err != nil {
		t.Fatal("远程自定义证书文件名未生成")
	}
	if string(certData) != string(b.FullchainPEM) {
		t.Error("远程证书内容与本地不一致")
	}
	if _, err := os.Stat(filepath.Join(srv.dir, "aijijie.com.key")); err != nil {
		t.Error("远程自定义私钥文件名未生成")
	}
	// 默认名不应生成
	if _, err := os.Stat(filepath.Join(srv.dir, "fullchain.pem")); !os.IsNotExist(err) {
		t.Error("默认文件名 fullchain.pem 不应生成")
	}
}

// TestNewSSHDefaults 验证默认文件名回退。
func TestNewSSHDefaults(t *testing.T) {
	s, err := NewSSH(SSHConfig{Host: "h", User: "u", RemotePath: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if s.certPath() != "/x/fullchain.pem" {
		t.Errorf("certPath 默认错误: %s", s.certPath())
	}
	if s.keyPath() != "/x/privkey.pem" {
		t.Errorf("keyPath 默认错误: %s", s.keyPath())
	}
}

// TestNewSSHBadFilename 验证非法文件名被拒绝(路径穿越)。
func TestNewSSHBadFilename(t *testing.T) {
	_, err := NewSSH(SSHConfig{Host: "h", User: "u", RemotePath: "/x", CertFilename: "../evil.pem"})
	if err == nil {
		t.Fatal("包含 .. 的文件名应被拒绝")
	}
	_, err = NewSSH(SSHConfig{Host: "h", User: "u", RemotePath: "/x", KeyFilename: "a/b.pem"})
	if err == nil {
		t.Fatal("含路径分隔符的文件名应被拒绝")
	}
}
