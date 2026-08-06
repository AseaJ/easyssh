//go:build !windows

package app

// SetAutostart 启用/禁用用户级开机自启。
// 非 Windows 平台:无用户级 Run 键,静默忽略(返回 nil),避免 SaveConfig 等调用方在非 Windows 上报错。
func SetAutostart(enabled bool) error {
	return nil
}

// AutostartEnabled 查询用户级开机自启当前状态。
// 非 Windows 平台固定返回 false。
func AutostartEnabled() bool {
	return false
}
