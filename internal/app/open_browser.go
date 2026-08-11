package app

import (
	"os/exec"
	"runtime"
)

// openBrowser 用系统默认浏览器打开 URL(跨平台)。
// 失败静默(仅记录调用方日志,不阻断流程)。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		// 调用方已记录日志,此处忽略
		_ = err
	}
}
