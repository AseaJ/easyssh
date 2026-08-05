package deploy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/md5"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/asea/easyssh/internal/certmgr"
	"github.com/asea/easyssh/internal/config"
	"github.com/asea/easyssh/internal/testutil"
)

// sshTestServer 是内存 SSH 服务器,支持 sftp subsystem 与 exec 命令。
type sshTestServer struct {
	addr      string
	clientKey ssh.Signer
	dir       string
	mu        sync.Mutex
	cmdLog    []string
	failCmd   bool
}

func startSSHServer(t *testing.T) (srv *sshTestServer, keyPath string, khPath string) {
	t.Helper()
	hostKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}

	srv = &sshTestServer{clientKey: clientSigner, dir: t.TempDir()}
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(clientSigner.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("未知密钥")
		},
	}
	config.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.addr = ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(config, conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })

	keyPath = filepath.Join(t.TempDir(), "id_test")
	der, _ := x509.MarshalECPrivateKey(clientKey)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	// 生成与测试服务器匹配的 known_hosts 文件(供客户端严格校验)
	khPath = filepath.Join(t.TempDir(), "known_hosts")
	khLine := knownhosts.Line([]string{knownhosts.Normalize(srv.addr)}, hostSigner.PublicKey())
	if err := os.WriteFile(khPath, []byte(khLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return srv, keyPath, khPath
}

func (s *sshTestServer) handleConn(config *ssh.ServerConfig, conn net.Conn) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		switch newChan.ChannelType() {
		case "session":
			go s.handleSession(newChan)
		default:
			newChan.Reject(ssh.UnknownChannelType, "仅支持 session")
		}
	}
}

func (s *sshTestServer) handleSession(newChan ssh.NewChannel) {
	ch, reqs, err := newChan.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			var payload struct{ Name string }
			ssh.Unmarshal(req.Payload, &payload)
			if payload.Name != "sftp" {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			srv, err := sftp.NewServer(ch)
			if err != nil {
				continue
			}
			srv.Serve()
			return
		case "exec":
			var payload struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				continue
			}
			s.mu.Lock()
			s.cmdLog = append(s.cmdLog, payload.Command)
			s.mu.Unlock()
			req.Reply(true, nil)
			code := s.handleCommand(ch, payload.Command)
			// mock 服务器需要给客户端时间读取 stdout 数据,再发 exit-status
			time.Sleep(200 * time.Millisecond)
			ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
			return
		default:
			req.Reply(false, nil)
		}
	}
}

// handleCommand 模拟远程 shell 命令:cat 写文件 / md5sum / mv / cp / 其他。
func (s *sshTestServer) handleCommand(ch ssh.Channel, cmd string) uint32 {
	args := strings.Fields(strings.Trim(cmd, " \t"))
	if len(args) == 0 {
		return 0
	}
	switch args[0] {
	case "cat":
		if len(args) >= 3 && args[1] == ">" {
			p := strings.Trim(args[2], "'")
			data, _ := io.ReadAll(ch)
			_ = os.WriteFile(filepath.Join(s.dir, path.Base(p)), data, 0o600)
			return 0
		}
	case "md5sum":
		if len(args) >= 2 {
			p := strings.Trim(args[1], "'")
			data, err := os.ReadFile(filepath.Join(s.dir, path.Base(p)))
			if err != nil {
				return 1
			}
			sum := md5.Sum(data)
			fmt.Fprintf(ch, "%x  %s\n", sum, p)
			return 0
		}
	case "mv", "cp":
		for _, seg := range strings.Split(cmd, "&&") {
			parts := strings.Fields(strings.TrimSpace(seg))
			if len(parts) < 4 || parts[1] != "-f" {
				continue
			}
			src := strings.Trim(parts[2], "'")
			dst := strings.Trim(parts[3], "'")
			if args[0] == "mv" {
				_ = os.Rename(filepath.Join(s.dir, path.Base(src)), filepath.Join(s.dir, path.Base(dst)))
			} else {
				data, err := os.ReadFile(filepath.Join(s.dir, path.Base(src)))
				if err == nil {
					_ = os.WriteFile(filepath.Join(s.dir, path.Base(dst)), data, 0o600)
				}
			}
		}
		return 0
	}
	s.mu.Lock()
	fail := s.failCmd
	s.mu.Unlock()
	if fail {
		ch.Write([]byte("模拟命令失败"))
		return 1
	}
	ch.Write([]byte("ok"))
	return 0
}

func sshBundle() *certmgr.Bundle {
	leaf, key := testutil.GenSelfSigned([]string{"a.com"}, time.Now().Add(60*24*time.Hour))
	return &certmgr.Bundle{
		Name:          "a",
		Domains:       []string{"a.com"},
		LeafPEM:       leaf,
		FullchainPEM:  leaf,
		PrivateKeyPEM: key,
		NotAfter:      time.Now().Add(60 * 24 * time.Hour),
		Fingerprint:   certmgr.FingerprintOf(leaf),
	}
}

func sshDeployer(t *testing.T, srv *sshTestServer, keyPath, khPath, reloadCmd string) *SSH {
	t.Helper()
	host, port, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	var p int
	fmt.Sscanf(port, "%d", &p)
	d, err := NewSSH(SSHConfig{
		Host:       host,
		Port:       p,
		User:       "test",
		Key:        keyPath,
		KnownHosts: khPath,
		RemotePath: srv.dir,
		ReloadCmd:  reloadCmd,
		Timeout:    10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMockStdout(t *testing.T) {
	srv, keyPath, khPath := startSSHServer(t)
	host, port, _ := net.SplitHostPort(srv.addr)
	var p int
	fmt.Sscanf(port, "%d", &p)
	s := &SSH{cfg: SSHConfig{Host: host, Port: p, User: "test", Key: keyPath, KnownHosts: khPath, RemotePath: srv.dir, Timeout: 10 * time.Second}}
	client, err := s.dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	out, err := s.runRemote(client, "echo hello")
	if err != nil {
		t.Fatalf("runRemote: %v", err)
	}
	if string(out) != "ok" {
		t.Fatalf("stdout 回传异常: %q", string(out))
	}
}

func TestSSHDeploySuccess(t *testing.T) {
	srv, keyPath, khPath := startSSHServer(t)
	d := sshDeployer(t, srv, keyPath, khPath, "nginx -t && nginx -s reload")
	b := sshBundle()

	if err := d.Deploy(context.Background(), b); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	// 远程文件已生成且内容一致
	certData, err := os.ReadFile(filepath.Join(srv.dir, "fullchain.pem"))
	if err != nil {
		t.Error("远程 fullchain.pem 未生成")
	}
	if string(certData) != string(b.FullchainPEM) {
		t.Error("远程证书内容与本地不一致")
	}
	if _, err := os.Stat(filepath.Join(srv.dir, "privkey.pem")); err != nil {
		t.Error("远程 privkey.pem 未生成")
	}
	// 无临时文件残留
	if _, err := os.Stat(filepath.Join(srv.dir, "fullchain.pem.tmp")); !os.IsNotExist(err) {
		t.Error("临时文件残留")
	}
	// reload 为最后一条远程命令
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.cmdLog) == 0 || srv.cmdLog[len(srv.cmdLog)-1] != "nginx -t && nginx -s reload" {
		t.Errorf("远程命令记录异常(应包含 reload): %v", srv.cmdLog)
	}
}

func TestSSHDeployIdempotent(t *testing.T) {
	srv, keyPath, khPath := startSSHServer(t)
	d := sshDeployer(t, srv, keyPath, khPath, "nginx -s reload")

	b := sshBundle()
	b.Meta.DeployedFingerprint = b.Fingerprint
	b.Meta.DeployedTargets = []string{"ssh"}

	if err := d.Deploy(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.cmdLog) != 0 {
		t.Errorf("幂等场景不应执行远程命令: %v", srv.cmdLog)
	}
}

func TestSSHDeployReloadFailRollback(t *testing.T) {
	srv, keyPath, khPath := startSSHServer(t)
	oldCert := []byte("OLD REMOTE CERT")
	os.WriteFile(filepath.Join(srv.dir, "fullchain.pem"), oldCert, 0o644)

	srv.mu.Lock()
	srv.failCmd = true
	srv.mu.Unlock()

	d := sshDeployer(t, srv, keyPath, khPath, "nginx -t && nginx -s reload")
	err := d.Deploy(context.Background(), sshBundle())
	if err == nil {
		t.Fatal("远程命令失败应返回错误")
	}
	// 旧文件应回滚(从 .bak 恢复)
	data, _ := os.ReadFile(filepath.Join(srv.dir, "fullchain.pem"))
	if string(data) != string(oldCert) {
		t.Error("远程命令失败后未回滚旧证书")
	}
}

func TestNewDeployerHostRef(t *testing.T) {
	hosts := []config.HostConfig{
		{Name: "prod", Host: "10.0.0.1", Port: 22, User: "deploy", Key: "/k", RemotePath: "/etc/ssl", ReloadCmd: "nginx -s reload"},
	}
	d, err := NewDeployer(config.DeployConfig{Type: "ssh", HostRef: "prod"}, hosts)
	if err != nil {
		t.Fatalf("host_ref 解析失败: %v", err)
	}
	ssh, ok := d.(*SSH)
	if !ok {
		t.Fatalf("类型 = %T,期望 *SSH", d)
	}
	if ssh.cfg.Host != "10.0.0.1" || ssh.cfg.User != "deploy" || ssh.cfg.RemotePath != "/etc/ssl" {
		t.Errorf("host_ref 合并异常: %+v", ssh.cfg)
	}
	d2, err := NewDeployer(config.DeployConfig{Type: "ssh", HostRef: "prod", Host: "10.0.0.2"}, hosts)
	if err != nil {
		t.Fatal(err)
	}
	if d2.(*SSH).cfg.Host != "10.0.0.2" {
		t.Errorf("内联覆盖失败: %+v", d2.(*SSH).cfg)
	}
	if _, err := NewDeployer(config.DeployConfig{Type: "ssh", HostRef: "nope"}, hosts); err == nil {
		t.Fatal("引用不存在的 host 应报错")
	}
}

func TestWebhookDeployer(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	d, err := NewWebhookDeployer(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b := sshBundle()
	if err := d.Deploy(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "cert.updated") || !strings.Contains(got, b.Fingerprint) {
		t.Errorf("webhook payload 异常: %s", got)
	}
	b.Meta.DeployedFingerprint = b.Fingerprint
	b.Meta.DeployedTargets = []string{"webhook"}
	if err := d.Deploy(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func TestPingOK(t *testing.T) {
	srv, keyPath, khPath := startSSHServer(t)
	host, portStr, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)
	if err := Ping(context.Background(), SSHConfig{Host: host, Port: port, User: "test", Key: keyPath, KnownHosts: khPath}); err != nil {
		t.Fatalf("Ping 应成功: %v", err)
	}
}

func TestPingConnectionRefused(t *testing.T) {
	// 监听后立即关闭,端口不可达
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if err := Ping(context.Background(), SSHConfig{Host: host, Port: port, User: "u", Key: "/nonexistent"}); err == nil {
		t.Fatal("连接拒绝应报错")
	}
}

// TestKnownHostsRequired:未配置 known_hosts 必须拒绝连接(安全默认)。
func TestKnownHostsRequired(t *testing.T) {
	srv, keyPath, _ := startSSHServer(t)
	host, portStr, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)
	err = Ping(context.Background(), SSHConfig{Host: host, Port: port, User: "test", Key: keyPath})
	if err == nil {
		t.Fatal("未配置 known_hosts 应拒绝连接(防中间人)")
	}
	if !strings.Contains(err.Error(), "known_hosts") {
		t.Errorf("错误信息应说明需要 known_hosts: %v", err)
	}
}

// TestKnownHostsMismatch:known_hosts 中的指纹与服务器不匹配时必须拒绝。
func TestKnownHostsMismatch(t *testing.T) {
	srv, keyPath, _ := startSSHServer(t)
	// 生成一个"错误"的 known_hosts(随机密钥,与服务器不符)
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherSigner, err := ssh.NewSignerFromKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	khPath := filepath.Join(t.TempDir(), "known_hosts_wrong")
	line := knownhosts.Line([]string{knownhosts.Normalize(srv.addr)}, otherSigner.PublicKey())
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	host, portStr, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)
	err = Ping(context.Background(), SSHConfig{Host: host, Port: port, User: "test", Key: keyPath, KnownHosts: khPath})
	if err == nil {
		t.Fatal("known_hosts 指纹不匹配应拒绝连接")
	}
}

// TestHostKeyCallbackNoKnownHosts:hostKeyCallback 在未配置时直接报错,不发起网络连接。
func TestHostKeyCallbackNoKnownHosts(t *testing.T) {
	s := &SSH{cfg: SSHConfig{Host: "h", User: "u"}}
	if _, err := s.hostKeyCallback(); err == nil {
		t.Fatal("未配置 known_hosts 应返回错误")
	}
	// 配置不存在的 known_hosts 文件也应报错
	s2 := &SSH{cfg: SSHConfig{Host: "h", User: "u", KnownHosts: "/nonexistent/known_hosts"}}
	if _, err := s2.hostKeyCallback(); err == nil {
		t.Fatal("known_hosts 文件不存在应报错")
	}
}
