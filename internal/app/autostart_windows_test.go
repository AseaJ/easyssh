//go:build windows

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestAutostartCommandPointsToRealExe 验证:测试/开发进程(app.test.exe,位于
// %TEMP%\go-build... 临时目录)调用 autostartCommand 时必须拒绝,绝不返回
// 临时目录里的路径——这类路径重启后失效,是历史上开机自启"静默失效"的根因。
func TestAutostartCommandPointsToRealExe(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := autostartCommand(); err == nil {
		t.Fatalf("测试进程(%s)不应生成可用的自启命令,必须拒绝", exe)
	}
	// 同时验证:即使测试进程旁被放了一个 easyssh.exe,解析也必须拒绝(仍指向临时目录)
	dir := filepath.Dir(exe)
	realExe := filepath.Join(dir, autostartExeBase)
	if _, err := os.Stat(realExe); err != nil {
		f, err := os.CreateTemp(dir, "easyssh-probe-*.tmp")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		if err := os.Rename(f.Name(), realExe); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(realExe)
	}
	if _, err := autostartCommand(); err == nil {
		t.Fatalf("临时目录下的 easyssh.exe 不应被写入自启命令,必须拒绝")
	}
}

// TestSetAutostartFromTestProcessRefuses 验证:测试进程启用自启时必须拒绝,
// 不向 HKCU Run 键写入任何值,防止覆盖正式自启为无效路径。
func TestSetAutostartFromTestProcessRefuses(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(filepath.Base(exe), autostartExeBase) {
		t.Skip("测试进程名即为 easyssh.exe,跳过(go test 场景不会出现)")
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("打开自启注册表键: %v", err)
	}
	defer k.Close()
	// 记录原值,测试结束后恢复,避免污染用户真实自启配置
	old, _, err := k.GetStringValue(autostartValueName)
	hadOld := err == nil
	defer func() {
		if hadOld {
			k.SetStringValue(autostartValueName, old)
		} else {
			k.DeleteValue(autostartValueName)
		}
	}()

	if err := SetAutostart(true); err == nil {
		t.Fatal("测试进程启用自启应失败(不应写入临时路径)")
	}
	if _, _, err := k.GetStringValue(autostartValueName); err == nil {
		t.Fatal("测试进程不应向自启注册表写入任何值")
	}
}
