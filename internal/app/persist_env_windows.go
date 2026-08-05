//go:build windows

package app

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	sendMessageTimeout = user32.NewProc("SendMessageTimeoutW")
)

const (
	hwndBroadcast    = 0xffff
	wmSettingChange  = 0x001A
	smtpAbortIfHung  = 0x0002
)

// PersistEnv 把密钥写入系统环境变量并持久化(Windows 写注册表,其他平台仅进程内)。
// 供 CLI import 与 bundle 导入回调复用。
func PersistEnv(name, value string) error {
	if err := os.Setenv(name, value); err != nil {
		return err
	}
	return persistEnvPlatform(name, value)
}

// persistEnvPlatform 写用户环境变量注册表,避免 setx 弹控制台窗口;
// 写完后广播 WM_SETTINGCHANGE,通知系统刷新环境(新启动的进程才能继承)。
func persistEnvPlatform(name, value string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开环境变量注册表: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(name, value); err != nil {
		return fmt.Errorf("写入注册表 %s: %w", name, err)
	}
	broadcastEnvChanged()
	return nil
}

// broadcastEnvChanged 广播环境变量变更事件。
func broadcastEnvChanged() {
	env, _ := syscall.UTF16PtrFromString("Environment")
	sendMessageTimeout.Call(
		uintptr(hwndBroadcast), uintptr(wmSettingChange), 0,
		uintptr(unsafe.Pointer(env)), uintptr(smtpAbortIfHung), 5000, 0,
	)
}

// LoadPersistedEnv 从用户环境变量注册表加载缺失的变量到当前进程。
// 兜底机制:即使系统未广播刷新,go-zs 也能拿到先前保存的密钥。
func LoadPersistedEnv() {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return
	}
	for _, n := range names {
		if _, ok := os.LookupEnv(n); ok {
			continue // 进程已有则保留(避免覆盖)
		}
		if v, _, err := k.GetStringValue(n); err == nil {
			_ = os.Setenv(n, v)
		}
	}
}
