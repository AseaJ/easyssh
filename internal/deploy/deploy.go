package deploy

import (
	"context"
	"fmt"

	"go-zs/internal/certmgr"
	"go-zs/internal/config"
)

// Deployer 是部署目标抽象。
type Deployer interface {
	Name() string
	Deploy(ctx context.Context, bundle *certmgr.Bundle) error
}

// NewDeployer 按配置创建部署目标。hosts 提供可复用的 SSH 目标定义,
// ssh 部署可通过 host_ref 引用,内联字段可覆盖引用中的对应项。
func NewDeployer(cfg config.DeployConfig, hosts []config.HostConfig) (Deployer, error) {
	switch cfg.Type {
	case "nginx":
		return NewNginx(NginxConfig{
			CertPath:  cfg.CertPath,
			KeyPath:   cfg.KeyPath,
			TestCmd:   cfg.TestCmd,
			ReloadCmd: cfg.ReloadCmd,
		})
	case "file":
		return NewFile(FileConfig{Dir: cfg.Dir})
	case "ssh":
		return newSSHDeployer(cfg, hosts)
	case "webhook":
		return NewWebhookDeployer(cfg.URL)
	default:
		return nil, fmt.Errorf("未知 deploy 类型 %q", cfg.Type)
	}
}

// newSSHDeployer 解析 host_ref:引用 hosts 定义后,内联字段覆盖默认值。
func newSSHDeployer(cfg config.DeployConfig, hosts []config.HostConfig) (Deployer, error) {
	base := config.HostConfig{
		Host: cfg.Host, Port: cfg.Port, User: cfg.User,
		Key: cfg.Key, KnownHosts: cfg.KnownHosts, RemotePath: cfg.RemotePath, ReloadCmd: cfg.ReloadCmd,
		CertFilename: cfg.CertFilename, KeyFilename: cfg.KeyFilename,
	}
	if cfg.HostRef != "" {
		found := false
		for i := range hosts {
			if hosts[i].Name == cfg.HostRef {
				h := hosts[i]
				if base.Host == "" {
					base.Host = h.Host
				}
				if base.Port == 0 {
					base.Port = h.Port
				}
				if base.User == "" {
					base.User = h.User
				}
				if base.Key == "" {
					base.Key = h.Key
				}
				if base.KnownHosts == "" {
					base.KnownHosts = h.KnownHosts
				}
				if base.RemotePath == "" {
					base.RemotePath = h.RemotePath
				}
				if base.ReloadCmd == "" {
					base.ReloadCmd = h.ReloadCmd
				}
				if base.CertFilename == "" {
					base.CertFilename = h.CertFilename
				}
				if base.KeyFilename == "" {
					base.KeyFilename = h.KeyFilename
				}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("引用的 host %q 不存在", cfg.HostRef)
		}
	}
	if base.Host == "" || base.User == "" {
		return nil, fmt.Errorf("ssh 部署缺少 host/user(host_ref=%s)", cfg.HostRef)
	}
	return NewSSH(SSHConfig{
		Host:         base.Host,
		Port:         base.Port,
		User:         base.User,
		Key:          base.Key,
		KnownHosts:   base.KnownHosts,
		RemotePath:   base.RemotePath,
		ReloadCmd:    base.ReloadCmd,
		CertFilename: base.CertFilename,
		KeyFilename:  base.KeyFilename,
	})
}
