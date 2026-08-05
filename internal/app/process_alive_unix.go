//go:build !windows

package app

import (
	"os"
	"syscall"
)

// processAlive 判断 PID 对应的进程是否存活。
// 非 Windows 平台用信号 0 探测:进程存在则成功。
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Unix:signal 0 仅做存在性探测,不发送真实信号。
	// 对当前用户无权限时误报"存活",但那是权限问题,保守处理即可。
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
