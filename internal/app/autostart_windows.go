//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// autostartRunKey 是 Windows 用户级开机自启注册表键(HKCU\...\Run)。
// 仅当前用户登录时生效,无需管理员权限。
const autostartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartValueName 是注册表值名(与程序名一致,便于识别)。
const autostartValueName = "easyssh"

var errAutostartUnsupported = errors.New("开机自启仅支持 Windows")

// autostartExeBase 是自启注册表值中只允许指向的程序名。
// 自启命令行写死指向 easyssh.exe,绝不写入测试进程(app.test.exe)、
// 临时目录或其他可执行文件的路径,否则该路径一旦失效(如 %TEMP% 被清理)
// 开机自启就会静默失败,且下次保存配置时还会把坏路径再次固化。
const autostartExeBase = "easyssh.exe"

// resolveAutostartExe 返回要写入自启注册表的真实 exe 路径:
//   - 当前程序名是 easyssh.exe 且不在临时目录:直接用当前程序路径(正常 GUI 场景);
//   - 其他程序(测试/开发进程)或在临时目录:在同一目录下找 easyssh.exe(构建产物旁启动),
//     仍不存在/仍在临时目录则拒绝写入,防止把临时测试路径写进正式自启键。
func resolveAutostartExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前程序: %w", err)
	}
	if isLikelyTempDir(filepath.Dir(exe)) {
		exe = filepath.Join(filepath.Dir(exe), autostartExeBase)
	}
	if strings.EqualFold(filepath.Base(exe), autostartExeBase) {
		if isLikelyTempDir(filepath.Dir(exe)) {
			return "", fmt.Errorf("程序位于临时目录(%s),重启后路径会失效,拒绝写入自启注册表", filepath.Dir(exe))
		}
		return exe, nil
	}
	// 非 easyssh.exe(如 app.test.exe):尝试用同目录下的正式程序
	candidate := filepath.Join(filepath.Dir(exe), autostartExeBase)
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() && !isLikelyTempDir(filepath.Dir(candidate)) {
		return candidate, nil
	}
	return "", fmt.Errorf("当前程序不是 %s,且同目录下未找到正式程序,拒绝写入自启注册表(避免覆盖正式自启为无效路径)", autostartExeBase)
}

// isLikelyTempDir 判断目录是否在系统临时目录下(如 %TEMP%\go-build...),
// 这类路径在重启/清理后会失效,不能作为开机自启目标。
func isLikelyTempDir(dir string) bool {
	d := filepath.Clean(strings.ToLower(dir))
	for _, t := range []string{
		strings.ToLower(os.TempDir()),
		strings.ToLower(filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local", "Temp")),
	} {
		if t == "" {
			continue
		}
		t = filepath.Clean(t)
		if d == t || strings.HasPrefix(d, t+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// autostartCommand 返回自启要执行的完整命令行:
// 带引号的 exe 路径 + --autostart 标志,并 cd 到 exe 目录,保证相对配置路径解析正确。
func autostartCommand() (string, error) {
	exe, err := resolveAutostartExe()
	if err != nil {
		return "", err
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
