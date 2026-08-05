package deploy

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"go-zs/internal/certmgr"
)

// SSHConfig 是 ssh 部署目标配置。
type SSHConfig struct {
	Host         string
	Port         int
	User         string
	Key          string // 私钥路径(空则尝试 ssh-agent)
	KnownHosts   string // known_hosts 路径(推荐;为空则拒绝连接,防中间人)
	RemotePath   string // 远程目录(证书写到 <dir>/fullchain.pem、privkey.pem)
	ReloadCmd    string // 远程执行命令(如 "nginx -t && nginx -s reload")
	TestCmd      string // 远程校验命令(可选,reload_cmd 为空时使用)
	CertFilename string // 可选:远程证书文件名(默认 fullchain.pem)
	KeyFilename  string // 可选:远程私钥文件名(默认 privkey.pem)
	Timeout      time.Duration
}

// SSH 把证书通过 SSH exec 通道推送到远程服务器并执行 reload。
// 使用 exec + stdin 写文件(而非 sftp),兼容老版本 OpenSSH sftp-server。
type SSH struct {
	cfg SSHConfig
}

func NewSSH(cfg SSHConfig) (*SSH, error) {
	if cfg.Host == "" || cfg.User == "" || cfg.RemotePath == "" {
		return nil, errors.New("ssh 部署需要 host/user/remote_path")
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.CertFilename == "" {
		cfg.CertFilename = "fullchain.pem"
	}
	if cfg.KeyFilename == "" {
		cfg.KeyFilename = "privkey.pem"
	}
	for _, fn := range []string{cfg.CertFilename, cfg.KeyFilename} {
		if !validDeployFilename(fn) {
			return nil, fmt.Errorf("非法的证书文件名 %q(只允许文件名,不能含路径分隔符或 ..)", fn)
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &SSH{cfg: cfg}, nil
}

func (s *SSH) Name() string { return "ssh" }

// validDeployFilename 校验远程文件名安全(与 config 层校验一致,双保险)。
func validDeployFilename(name string) bool {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// Ping 测试 SSH 连接可用性:拨号 + 执行无害命令,验证认证、网络与远程 shell。
// 与部署不同,不要求 remote_path(仅供 GUI「测试连接」使用)。
func Ping(ctx context.Context, cfg SSHConfig) error {
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	s := &SSH{cfg: cfg}
	client, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	if out, err := s.runRemote(client, "echo go-zs-ok"); err != nil {
		return fmt.Errorf("远程命令执行失败: %v(输出: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// 远程路径必须用 POSIX 分隔符(/),不能用 filepath.Join(Windows 会产生反斜杠)。
func (s *SSH) certPath() string { return path.Join(s.cfg.RemotePath, s.cfg.CertFilename) }
func (s *SSH) keyPath() string  { return path.Join(s.cfg.RemotePath, s.cfg.KeyFilename) }

// Deploy 流程:连接 → 备份 → exec 写临时文件 → 原子替换 → md5 校验 → reload;失败回滚。
func (s *SSH) Deploy(ctx context.Context, bundle *certmgr.Bundle) error {
	if bundle == nil || len(bundle.FullchainPEM) == 0 || len(bundle.PrivateKeyPEM) == 0 {
		return errors.New("证书包不完整")
	}
	if bundle.Meta.DeployedFingerprint == bundle.Fingerprint &&
		contains(bundle.Meta.DeployedTargets, s.Name()) {
		return nil
	}
	log.Printf("[deploy:ssh] remote_path=%q host=%s user=%s 开始部署", s.cfg.RemotePath, s.cfg.Host, s.cfg.User)

	client, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	log.Printf("[deploy:ssh] 连接成功 %s", s.cfg.Host)

	// 创建远程目录 + 备份旧文件(忽略"文件不存在")
	if _, err := s.runRemote(client, "mkdir -p "+shellQuote(s.cfg.RemotePath)+
		" && cp -f "+shellQuote(s.certPath())+" "+shellQuote(s.certPath()+".bak")+" || true"+
		" && cp -f "+shellQuote(s.keyPath())+" "+shellQuote(s.keyPath()+".bak")+" || true"); err != nil {
		return fmt.Errorf("远程备份失败: %w", err)
	}
	log.Printf("[deploy:ssh] 备份完成")

	// exec 通道写临时文件(stdin 直传)
	certTmp := s.certPath() + ".tmp"
	keyTmp := s.keyPath() + ".tmp"
	if err := s.writeFileExec(client, certTmp, bundle.FullchainPEM); err != nil {
		s.restoreExec(client)
		return err
	}
	log.Printf("[deploy:ssh] 写入证书临时文件完成,字节数=%d", len(bundle.FullchainPEM))
	if err := s.writeFileExec(client, keyTmp, bundle.PrivateKeyPEM); err != nil {
		s.restoreExec(client)
		return err
	}
	log.Printf("[deploy:ssh] 写入私钥临时文件完成,字节数=%d", len(bundle.PrivateKeyPEM))

	// 原子替换
	if _, err := s.runRemote(client, "mv -f "+shellQuote(certTmp)+" "+shellQuote(s.certPath())+
		" && mv -f "+shellQuote(keyTmp)+" "+shellQuote(s.keyPath())); err != nil {
		s.restoreExec(client)
		return fmt.Errorf("远程替换失败,已回滚: %w", err)
	}
	log.Printf("[deploy:ssh] 远程替换完成")

	// md5 校验:确保远程文件真实落盘且与本地一致
	if err := s.verifyExec(client, bundle); err != nil {
		s.restoreExec(client)
		return err
	}
	log.Printf("[deploy:ssh] 远程 md5 校验通过")

	// 远程校验 + reload
	cmd := s.cfg.ReloadCmd
	if cmd == "" {
		cmd = s.cfg.TestCmd
	}
	if cmd == "" {
		cmd = "nginx -t && nginx -s reload"
	}
	log.Printf("[deploy:ssh] 执行远程命令: %s", cmd)
	if out, err := s.runRemote(client, cmd); err != nil {
		s.restoreExec(client)
		return fmt.Errorf("远程命令失败(%s),已回滚: %v;输出: %s", cmd, err, string(out))
	}
	log.Printf("[deploy:ssh] 远程命令成功")
	return nil
}

// writeFileExec 通过 SSH 会话 stdin 写文件(等价于 ssh host "cat > path")。
func (s *SSH) writeFileExec(client *ssh.Client, path string, data []byte) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("创建会话: %w", err)
	}
	defer session.Close()
	session.Stdin = bytes.NewReader(data)
	if err := session.Run("cat > " + shellQuote(path)); err != nil {
		return fmt.Errorf("写入远程文件 %s: %w", path, err)
	}
	return nil
}

// verifyExec 远程 md5sum 与本地对比,确认真实落盘。
func (s *SSH) verifyExec(client *ssh.Client, bundle *certmgr.Bundle) error {
	out, err := s.runRemote(client, "md5sum "+shellQuote(s.certPath()))
	if err != nil {
		return fmt.Errorf("远程 md5 校验失败: %w", err)
	}
	log.Printf("[deploy:ssh] md5 原始输出: %q", string(out))
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return errors.New("远程 md5 输出为空")
	}
	local := md5.Sum(bundle.FullchainPEM)
	localHex := hex.EncodeToString(local[:])
	log.Printf("[deploy:ssh] 校验: 远程 md5=%s 本地 md5=%s", fields[0], localHex)
	if fields[0] != localHex {
		return errors.New("远程证书与本地不一致,部署未生效,已回滚")
	}
	return nil
}

// restoreExec 失败时从 .bak 恢复(尽力而为)。
func (s *SSH) restoreExec(client *ssh.Client) {
	_, _ = s.runRemote(client, "cp -f "+shellQuote(s.certPath()+".bak")+" "+shellQuote(s.certPath())+" || true"+
		" && cp -f "+shellQuote(s.keyPath()+".bak")+" "+shellQuote(s.keyPath())+" || true")
}

func (s *SSH) dial(ctx context.Context) (*ssh.Client, error) {
	auths, err := s.authMethods()
	if err != nil {
		return nil, err
	}
	// 严格校验远程主机指纹(known_hosts):防中间人攻击。
	// 未配置 known_hosts 时拒绝连接,避免静默降级到不安全模式。
	hostKeyCallback, err := s.hostKeyCallback()
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{
		User:            s.cfg.User,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
		Timeout:         s.cfg.Timeout,
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	conn, err := (&net.Dialer{Timeout: s.cfg.Timeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 %s: %w", addr, err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH 握手失败: %w", err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// hostKeyCallback 返回基于 known_hosts 的严格主机密钥校验回调。
// 未配置 known_hosts 路径时返回错误(拒绝连接),确保安全默认。
func (s *SSH) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if s.cfg.KnownHosts == "" {
		return nil, errors.New("SSH 部署未配置 known_hosts 路径(防中间人攻击必需);" +
			"请在配置中设置 known_hosts,如: ssh-keyscan <host> >> /etc/go-zs/known_hosts")
	}
	khPath, err := expandHome(s.cfg.KnownHosts)
	if err != nil {
		return nil, fmt.Errorf("解析 known_hosts 路径: %w", err)
	}
	cb, err := knownhosts.New(khPath)
	if err != nil {
		return nil, fmt.Errorf("读取 known_hosts %s: %w", khPath, err)
	}
	return cb, nil
}

// expandHome 展开路径开头的 ~ 为当前用户主目录。
func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~"+string(filepath.Separator))), nil
	}
	return p, nil
}

func (s *SSH) authMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if s.cfg.Key != "" {
		data, err := os.ReadFile(s.cfg.Key)
		if err != nil {
			return nil, fmt.Errorf("读取私钥 %s: %w", s.cfg.Key, err)
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("解析私钥 %s: %w", s.cfg.Key, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if s.cfg.Key == "" {
		// 尝试 ssh-agent
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			conn, err := net.Dial("unix", sock)
			if err == nil {
				ag := agent.NewClient(conn)
				methods = append(methods, ssh.PublicKeysCallback(ag.Signers))
			}
		}
	}
	if len(methods) == 0 {
		return nil, errors.New("未配置 SSH 私钥且无法使用 ssh-agent")
	}
	return methods, nil
}

// runRemote 执行远程命令,返回 stdout+stderr 合并输出。
func (s *SSH) runRemote(client *ssh.Client, command string) ([]byte, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return session.CombinedOutput(command)
}

// shellQuote 用单引号包裹路径,防止 shell 解释。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

var _ = io.Discard
