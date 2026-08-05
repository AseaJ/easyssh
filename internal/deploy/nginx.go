package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go-zs/internal/certmgr"
)

// NginxConfig 是 nginx 部署目标配置。
type NginxConfig struct {
	CertPath   string // 证书落盘路径(必填)
	KeyPath    string // 私钥落盘路径(必填)
	TestCmd    string // 配置校验命令,默认 "nginx -t"
	ReloadCmd  string // 重载命令,默认 "nginx -s reload"
	Runner     CommandRunner
}

// Nginx 把证书复制到目标路径并触发校验 + reload,失败回滚。
type Nginx struct {
	cfg NginxConfig
}

func NewNginx(cfg NginxConfig) (*Nginx, error) {
	if cfg.CertPath == "" || cfg.KeyPath == "" {
		return nil, errors.New("nginx 部署需要 cert_path 与 key_path")
	}
	if cfg.TestCmd == "" {
		cfg.TestCmd = "nginx -t"
	}
	if cfg.ReloadCmd == "" {
		cfg.ReloadCmd = "nginx -s reload"
	}
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner{}
	}
	return &Nginx{cfg: cfg}, nil
}

func (n *Nginx) Name() string { return "nginx" }

// Deploy 流程:幂等检查 → 备份旧文件 → 原子写新文件 → nginx -t → reload;校验失败回滚。
func (n *Nginx) Deploy(ctx context.Context, bundle *certmgr.Bundle) error {
	if bundle == nil || len(bundle.FullchainPEM) == 0 || len(bundle.PrivateKeyPEM) == 0 {
		return errors.New("证书包不完整")
	}
	// 幂等:指纹未变且本目标已部署
	if bundle.Meta.DeployedFingerprint == bundle.Fingerprint &&
		contains(bundle.Meta.DeployedTargets, n.Name()) {
		return nil
	}

	// 备份旧文件(如存在)
	backups, err := n.backup()
	if err != nil {
		return err
	}

	// 原子写新文件
	if err := atomicWriteFile(n.cfg.CertPath, bundle.FullchainPEM, 0o644); err != nil {
		n.restore(backups)
		return fmt.Errorf("写入证书 %s: %w", n.cfg.CertPath, err)
	}
	if err := atomicWriteFile(n.cfg.KeyPath, bundle.PrivateKeyPEM, 0o600); err != nil {
		n.restore(backups)
		return fmt.Errorf("写入私钥 %s: %w", n.cfg.KeyPath, err)
	}

	// 校验配置
	name, args := runParts(n.cfg.TestCmd)
	if name == "" {
		n.restore(backups)
		return errors.New("test_cmd 为空")
	}
	if err := n.cfg.Runner.Run(ctx, name, args...); err != nil {
		n.restore(backups)
		return fmt.Errorf("nginx -t 校验失败,已回滚: %w", err)
	}

	// reload
	name, args = runParts(n.cfg.ReloadCmd)
	if err := n.cfg.Runner.Run(ctx, name, args...); err != nil {
		// reload 失败:证书文件已是新的,但进程未重载;不回滚(旧证书仍在进程内存中)
		return fmt.Errorf("reload 失败(证书已写入,建议手动重载): %w", err)
	}
	return nil
}

func (n *Nginx) backup() ([]string, error) {
	var backups []string
	for _, p := range []string{n.cfg.CertPath, n.cfg.KeyPath} {
		if _, err := os.Stat(p); err == nil {
			bak := p + ".bak"
			data, err := os.ReadFile(p)
			if err != nil {
				return backups, err
			}
			if err := os.WriteFile(bak, data, 0o600); err != nil {
				return backups, err
			}
			backups = append(backups, bak)
		}
	}
	return backups, nil
}

func (n *Nginx) restore(backups []string) {
	for _, bak := range backups {
		target := strings.TrimSuffix(bak, ".bak")
		if data, err := os.ReadFile(bak); err == nil {
			_ = atomicWriteFile(target, data, 0o600)
		}
	}
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
