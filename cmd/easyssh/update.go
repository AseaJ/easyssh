package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/asea/easyssh/internal/update"
)

// checkAndPrintUpdate 异步检查新版本并打印提示(serve 启动时调用,不阻塞启动)。
func checkAndPrintUpdate(ctx context.Context, logger *log.Logger) {
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		info, err := update.Latest(cctx, version)
		if err != nil {
			logger.Printf("检查更新失败: %v", err)
			return
		}
		if info.HasUpdate {
			logger.Printf("发现新版本 %s(当前 %s),更新方式: easyssh update 或 %s",
				info.Latest, info.Current, update.ReleasesURL)
		}
	}()
}

func newUpdateCmd() *cobra.Command {
	var (
		checkOnly bool
		yes       bool
		targetDir string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "检查并更新到最新版本(下载 + sha256 校验 + 替换)",
		Long: "检查 GitHub Releases 最新版本,下载对应平台的二进制,校验 sha256 后替换当前程序。\n" +
			"  easyssh update          检测到新版本则下载并替换\n" +
			"  easyssh update --check  仅检查是否有新版本,不下载\n" +
			"  --yes 跳过确认提示(CI/无人值守场景)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			info, err := update.Latest(ctx, version)
			if err != nil {
				return err
			}
			fmt.Printf("当前版本 : %s\n", info.Current)
			fmt.Printf("最新版本 : %s\n", info.Latest)
			fmt.Printf("发布页   : %s\n", info.ReleaseURL)
			if !info.HasUpdate {
				fmt.Printf("已是最新版本,无需更新\n")
				return nil
			}
			if checkOnly {
				fmt.Printf("检测到新版本 %s,可执行 `easyssh update` 更新\n", info.Latest)
				return nil
			}
			return doUpdate(ctx, info, yes, targetDir)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "仅检查是否有新版本,不下载")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "跳过确认提示")
	cmd.Flags().StringVar(&targetDir, "dir", "", "安装目录(默认当前程序所在目录)")
	return cmd
}

// doUpdate 执行下载、校验与替换。
func doUpdate(ctx context.Context, info *update.Info, yes bool, targetDir string) error {
	if targetDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("定位当前程序: %w", err)
		}
		targetDir = filepath.Dir(exe)
	}
	if !yes {
		if !confirm(fmt.Sprintf("将下载 %s 并替换当前程序,是否继续?", info.Latest)) {
			fmt.Println("已取消")
			return nil
		}
	}

	// 获取完整 release(含 assets 列表)
	fullCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rel, err := update.LatestRelease(fullCtx)
	if err != nil {
		return err
	}

	assetName := update.AssetName()
	if _, ok := update.FindAsset(rel, assetName); !ok {
		return fmt.Errorf("最新版本 %s 未提供当前平台二进制 %s", info.Latest, assetName)
	}

	// 下载 → sha256 校验 → 安装到目标目录
	newPath := filepath.Join(targetDir, "easyssh.update.exe")
	_ = os.Remove(newPath)
	fmt.Printf("下载 %s ...\n", assetName)
	if err := update.UpdateBinary(ctx, rel, rel.TagName, newPath); err != nil {
		return err
	}
	fmt.Printf("sha256 校验通过\n")

	// 替换当前程序
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位当前程序: %w", err)
	}
	if err := replaceExecutable(exe, newPath); err != nil {
		return err
	}
	fmt.Printf("更新完成。当前程序已被替换为 %s,请重新运行 easyssh 使新版本生效。\n", info.Latest)
	return nil
}

// replaceExecutable 用 newExe 替换当前运行的程序 exe。
// Windows 上无法覆盖正在运行的文件,采用"退出后由脚本替换"模式;
// 其他平台直接 rename。
func replaceExecutable(exe, newExe string) error {
	if runtime.GOOS == "windows" {
		dir := filepath.Dir(exe)
		oldName := filepath.Base(exe)
		script := filepath.Join(dir, "update-easyssh.bat")
		content := buildUpdateScript(exe, newExe)
		if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
			return fmt.Errorf("写入更新脚本: %w", err)
		}
		if err := startDetachedCmd(script); err != nil {
			return fmt.Errorf("启动更新脚本: %w", err)
		}
		fmt.Printf("已准备更新脚本(等 %s 进程退出后自动替换)\n", oldName)
		return nil
	}
	if err := os.Rename(newExe, exe); err != nil {
		return fmt.Errorf("替换程序失败: %w", err)
	}
	return nil
}

// buildUpdateScript 生成 Windows 批处理:等当前进程退出 → 备份旧 exe → 用新 exe 替换 → 自删脚本。
// 所有嵌入路径都经过 cmdEscape 转义,防止路径中的 & | < > ^ % 等 cmd 元字符被解释为命令。
func buildUpdateScript(oldExe, newExe string) string {
	oldName := filepath.Base(oldExe)
	backup := filepath.Join(filepath.Dir(oldExe), "easyssh.old.exe")
	dir := filepath.Dir(oldExe)
	var sb strings.Builder
	sb.WriteString("@echo off\r\n")
	sb.WriteString("cd /d \"" + cmdEscape(dir) + "\"\r\n")
	sb.WriteString(":wait\r\n")
	sb.WriteString("tasklist /FI \"IMAGENAME eq " + cmdEscape(oldName) + "\" 2>nul | find /I \"" + cmdEscape(oldName) + "\" >nul\r\n")
	sb.WriteString("if %errorlevel%==0 (timeout /t 1 /nobreak >nul & goto :wait)\r\n")
	sb.WriteString("del /f /q \"" + cmdEscape(backup) + "\" >nul 2>&1\r\n")
	sb.WriteString("ren \"" + cmdEscape(oldExe) + "\" " + cmdEscape("easyssh.old.exe") + "\r\n")
	sb.WriteString("if errorlevel 1 goto :fail\r\n")
	sb.WriteString("move /y \"" + cmdEscape(newExe) + "\" \"" + cmdEscape(oldExe) + "\"\r\n")
	sb.WriteString("if errorlevel 1 goto :fail\r\n")
	sb.WriteString("del /f /q \"" + cmdEscape(backup) + "\" >nul 2>&1\r\n")
	sb.WriteString("del /f /q \"%~f0\" >nul 2>&1\r\n")
	sb.WriteString("exit /b 0\r\n")
	sb.WriteString(":fail\r\n")
	// 回滚:新文件未到位时,若备份存在则恢复旧 exe
	sb.WriteString("if not exist \"" + cmdEscape(oldExe) + "\" (if exist \"" + cmdEscape(backup) + "\" move /y \"" + cmdEscape(backup) + "\" \"" + cmdEscape(oldExe) + "\" >nul 2>&1)\r\n")
	sb.WriteString("del /f /q \"" + cmdEscape(backup) + "\" >nul 2>&1\r\n")
	sb.WriteString("del /f /q \"%~f0\" >nul 2>&1\r\n")
	sb.WriteString("exit /b 1\r\n")
	return sb.String()
}

// cmdEscape 对字符串做 cmd.exe 转义,使其可安全放在引号内或作为命令参数:
//   - % → %%  (百分号在批处理中需成对)
//   - ^ → ^^  (转义符本身)
//   - & | < > ( ) 后跟 ^ 前缀
// 注意:字符串最终会被双引号包裹,引号本身不需要转义(cmd 对引号内的 " 用 "" 处理,但路径中极少出现)。
func cmdEscape(s string) string {
	replacer := strings.NewReplacer(
		"%", "%%",
		"^", "^^",
		"&", "^&",
		"|", "^|",
		"<", "^<",
		">", "^>",
		"(", "^(",
		")", "^)",
	)
	return replacer.Replace(s)
}

// startDetachedCmd 以独立进程启动脚本(不等待,不继承本进程句柄)。
// path 必须加引号包裹,防止含空格路径被 start 拆成多个参数。
func startDetachedCmd(path string) error {
	cmd := exec.Command("cmd", "/c", "start", "", "/b", "\""+path+"\"")
	return cmd.Start()
}

// confirm 提示 y/n 确认,返回是否继续。
func confirm(msg string) bool {
	fmt.Printf("%s [y/N]: ", msg)
	var s string
	_, err := fmt.Scanln(&s)
	if err != nil {
		return false
	}
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}
