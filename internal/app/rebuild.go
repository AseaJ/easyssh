package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// RebuildAndRestart 重新构建 GUI 并自动重启:
//  1. 在 wails 项目目录(cmd/go-zs-app,含 wails.json)执行 `wails build -o go-zs-new.exe`,
//     产物为 cmd/go-zs-app/build/bin/go-zs-new.exe,若与当前 exe 不同目录则复制过去
//  2. 在 exe 目录写入 restart-go-zs.bat:等当前进程退出后,用新 exe 替换旧 exe 并重新启动
//  3. 前端收到成功后调用 runtime.Quit() 退出,脚本自动接管重启
//
// 仅支持 Windows(桌面应用当前仅发布 Windows)。构建失败时不写脚本、不退出。
func (a *App) RebuildAndRestart() (string, error) {
	if runtime.GOOS != "windows" {
		return "", fmt.Errorf("重建并重启仅支持 Windows")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前程序: %w", err)
	}
	exeDir := filepath.Dir(exe)
	newExe := filepath.Join(exeDir, "go-zs-new.exe")

	// 1) 构建:wails build 必须在含 wails.json 的目录(cmd/go-zs-app)下运行,
	//    findProjectRoot 返回的是项目根(go.mod 所在目录),需拼出 wails 项目目录。
	projRoot, err := findProjectRoot()
	if err != nil {
		return "", err
	}
	appDir := filepath.Join(projRoot, "cmd", "go-zs-app")
	a.logger.Printf("开始重建 GUI(wails build),项目目录: %s", appDir)
	cmd := exec.Command("wails", "build", "-o", "go-zs-new.exe")
	cmd.Dir = appDir
	// Wails 构建会输出大量日志,捕获到变量,失败时带回显
	out, err := cmd.CombinedOutput()
	if err != nil {
		a.logger.Printf("重建失败: %v", err)
		return "", fmt.Errorf("构建失败: %v\n%s", err, tailString(string(out), 1500))
	}
	// wails 固定把产物输出到 <项目目录>/build/bin/,与运行中 exe 同目录则无需复制
	built := filepath.Join(appDir, "build", "bin", "go-zs-new.exe")
	if _, err := os.Stat(built); err != nil {
		a.logger.Printf("重建完成但未找到产物 %s", built)
		return "", fmt.Errorf("构建完成但未找到产物 %s", built)
	}
	if err := ensureNewExe(built, newExe); err != nil {
		return "", err
	}
	a.logger.Printf("重建成功:%s", newExe)

	// 2) 写 restart-go-zs.bat(等当前进程退出 → 替换 → 启动 → 自删)
	script := filepath.Join(exeDir, "restart-go-zs.bat")
	content := buildRestartScript(exe, newExe)
	if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("写入重启脚本: %w", err)
	}
	// 3) 启动脚本(独立于本进程),随后前端调用 Quit()
	if err := startDetached(script); err != nil {
		return "", fmt.Errorf("启动重启脚本: %w", err)
	}
	return "重建完成,即将自动重启应用", nil
}

// findProjectRoot 从当前 exe 目录(build/bin)向上找含 go.mod 且其下存在
// cmd/go-zs-app/wails.json 的目录(即项目根)。最多向上找 6 层。
func findProjectRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	root := findProjectRootFrom(filepath.Dir(exe))
	if root == "" {
		return "", fmt.Errorf("未找到项目根(需含 go.mod 与 cmd/go-zs-app/wails.json),当前 exe: %s", exe)
	}
	return root, nil
}

// findProjectRootFrom 从指定 exe 目录向上扫描定位项目根(核心逻辑,便于测试)。
// 规则:向上找第一个含 go.mod 的目录,且其下存在 cmd/go-zs-app/wails.json。
func findProjectRootFrom(exeDir string) string {
	dir := exeDir
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "cmd", "go-zs-app", "wails.json")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// buildRestartScript 生成 Windows 批处理:等旧进程退出 → 替换 exe → 启动新进程 → 删除脚本。
func buildRestartScript(oldExe, newExe string) string {
	oldName := filepath.Base(oldExe)
	var sb strings.Builder
	sb.WriteString("@echo off\r\n")
	sb.WriteString("cd /d \"" + filepath.Dir(oldExe) + "\"\r\n")
	// 等待旧进程退出:循环探测进程存在性
	sb.WriteString(":wait\r\n")
	sb.WriteString("tasklist /FI \"IMAGENAME eq " + oldName + "\" 2>nul | find /I \"" + oldName + "\" >nul\r\n")
	sb.WriteString("if %errorlevel%==0 (timeout /t 1 /nobreak >nul & goto :wait)\r\n")
	// 替换:删旧 → 改名新
	sb.WriteString("del /f /q \"" + oldExe + "\" >nul 2>&1\r\n")
	sb.WriteString("move /y \"" + newExe + "\" \"" + oldExe + "\" >nul 2>&1\r\n")
	// 启动新进程(后台,不等待)
	sb.WriteString("start \"\" \"" + oldExe + "\"\r\n")
	// 自删脚本
	sb.WriteString("del /f /q \"%~f0\" >nul 2>&1\r\n")
	return sb.String()
}

// startDetached 以分离方式启动命令(不阻塞、不继承本进程生命周期)。
func startDetached(name string) error {
	cmd := exec.Command("cmd", "/c", "start", "", name)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ensureNewExe 确保 newExe 位置存在构建产物:若 built 与 newExe 是同一路径则跳过,否则复制。
func ensureNewExe(built, newExe string) error {
	if filepath.Clean(built) == filepath.Clean(newExe) {
		return nil
	}
	data, err := os.ReadFile(built)
	if err != nil {
		return fmt.Errorf("读取构建产物: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(newExe), 0o755); err != nil {
		return fmt.Errorf("创建 exe 目录: %w", err)
	}
	if err := os.WriteFile(newExe, data, 0o755); err != nil {
		return fmt.Errorf("复制构建产物到 exe 目录: %w", err)
	}
	return nil
}

// tailString 返回字符串末尾最多 n 字节(用于错误回显截断)。
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…(截断)…" + s[len(s)-n:]
}
