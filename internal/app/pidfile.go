// Package app 提供桌面应用与守护进程共用的应用级能力(PID 锁、桥接等)。
package app

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// AcquirePIDFile 以 PID 文件方式获取单实例锁。
// 返回的 release 函数释放锁并删除文件。
//
// 若 PID 文件中的进程已不存在(残留),会自动覆盖并重新获取,
// 避免崩溃后残留文件导致无法启动。
func AcquirePIDFile(path string) (release func(), err error) {
	if path == "" {
		// 未配置 PID 文件,不启用单实例保护
		return func() {}, nil
	}
	if data, rerr := os.ReadFile(path); rerr == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, perr := strconv.Atoi(pidStr); perr == nil && pid > 0 {
			if processAlive(pid) {
				return nil, fmt.Errorf("%w: 已有 easyssh 实例运行(PID %d,%s)", ErrLocked, pid, path)
			}
			// PID 已死:残留文件,覆盖继续
		}
		// 残留的非法内容,忽略并覆盖
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, fmt.Errorf("写入 PID 文件 %s: %w", path, err)
	}
	return func() {
		if cur, rerr := os.ReadFile(path); rerr == nil {
			if strings.TrimSpace(string(cur)) == strconv.Itoa(os.Getpid()) {
				_ = os.Remove(path)
			}
		}
	}, nil
}

// ErrLocked 表示单实例锁已被占用。
var ErrLocked = errors.New("单实例锁被占用")
