package app

import (
	"context"
	"fmt"

	"go-zs/internal/deploy"
)

// SSHTestParams 是 SSH 连接测试请求参数(来自配置表单当前输入,无需保存)。
type SSHTestParams struct {
	HostRef string `json:"host_ref,omitempty"` // 引用 hosts 定义名(优先)
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	User    string `json:"user,omitempty"`
	Key     string `json:"key,omitempty"`
}

// TestSSH 测试 SSH 连接:host_ref 引用 hosts 定义时以定义为基准、表单内联字段覆盖;
// 未引用时直接使用内联字段。拨号成功并执行无害命令后返回成功信息。
func (a *App) TestSSH(p SSHTestParams) (string, error) {
	cfg, _ := a.snap()
	if cfg == nil {
		return "", fmt.Errorf("配置未加载")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	host, port, user, key := p.Host, p.Port, p.User, p.Key
	if p.HostRef != "" {
		found := false
		for i := range cfg.Hosts {
			h := &cfg.Hosts[i]
			if h.Name == p.HostRef {
				found = true
				if host == "" {
					host = h.Host
				}
				if port == 0 {
					port = h.Port
				}
				if user == "" {
					user = h.User
				}
				if key == "" {
					key = h.Key
				}
				break
			}
		}
		if !found {
			return "", fmt.Errorf("引用的主机 %q 不存在", p.HostRef)
		}
	}
	if host == "" || user == "" {
		return "", fmt.Errorf("缺少主机地址或用户名")
	}
	if port == 0 {
		port = 22
	}
	if err := deploy.Ping(ctx, deploy.SSHConfig{
		Host: host, Port: port, User: user, Key: key,
	}); err != nil {
		return "", fmt.Errorf("连接失败: %w", err)
	}
	return fmt.Sprintf("连接成功 %s:%d(用户 %s)", host, port, user), nil
}
