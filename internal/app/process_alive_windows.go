//go:build windows

package app

import "golang.org/x/sys/windows"

// processAlive 判断 PID 对应的进程是否存活。
// Windows 用 OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION) 探测,不依赖 tasklist。
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	// 存在性即可:OpenProcess 成功即代表进程存在(可能有权限差异,退化为"存活"更安全)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil {
		return true // 无法查询时保守认为存活(避免误覆盖)
	}
	return exitCode == 259 // STILL_ACTIVE
}
