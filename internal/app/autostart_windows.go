//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// autostartRunKey 是 Windows 用户级开机自启注册表键(HKCU\...\Run)。
// 仅当前用户登录时生效,无需管理员权限。
const autostartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartValueName 是注册表值名(与程序名一致,便于识别)。
const autostartValueName = "easyssh"

var errAutostartUnsupported = errors.New("开机自启仅支持 Windows")

// autostartCommand 返回自启要执行的完整命令行:
// 带引号的 exe 路径 + --autostart 标志,并 cd 到 exe 目录,保证相对配置路径解析正确。
func autostartCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前程序: %w", err)
	}
	// 带引号包裹路径(可能含空格);--autostart 让 GUI 启动后直接进托盘后台
	return fmt.Sprintf(`"%s" --autostart`, exe), nil
}

// SetAutostart 启用/禁用用户级开机自启(写 HKCU Run 键)。
func SetAutostart(enabled bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开自启注册表键: %w", err)
	}
	defer k.Close()
	if !enabled {
		if err := k.DeleteValue(autostartValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("删除自启注册表值: %w", err)
		}
		return nil
	}
	cmd, err := autostartCommand()
	if err != nil {
		return err
	}
	if err := k.SetStringValue(autostartValueName, cmd); err != nil {
		return fmt.Errorf("写入自启注册表值: %w", err)
	}
	return nil
}

// AutostartEnabled 查询用户级开机自启当前是否已启用。
func AutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(autostartValueName)
	if err != nil || strings.TrimSpace(v) == "" {
		return false
	}
	return true
}
