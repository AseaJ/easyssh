// Package app 的托盘集成层:桌面端托盘图标的生命周期管理与回调。
// Windows 实现在 tray_windows.go(含 trayIcon/trayMenu/traySupported 等);
// 其他平台通过 tray_other.go 提供等价 stub,保证编译。
package app

import (
	"fmt"
	"sync"
)

// trayHandle 抽象托盘实例,避免平台差异泄漏到调用方。
// Windows 下为 *trayIcon;其他平台为 nil。
type trayHandle struct {
	icon *trayIcon

	mu     sync.Mutex
	stopCh chan struct{}
	done   chan struct{}
}

// startTray 启动托盘图标。onShow/onQuit 为菜单回调(Wails 主线程上下文调用)。
// Windows 失败时返回错误(调用方记录日志,不阻断启动)。
func (a *App) startTray() error {
	if !traySupported() {
		return nil // 非 Windows 无托盘
	}
	if a.tray != nil {
		return nil // 已启动
	}
	th := &trayHandle{
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	// onAction channel 缓冲 1,托盘线程非阻塞发送
	onAction := make(chan trayMenu, 1)
	icon, err := newTrayIcon(onAction)
	if err != nil {
		return fmt.Errorf("创建托盘图标失败: %w", err)
	}
	th.icon = icon
	a.tray = th

	// 监听托盘菜单回调(独立 goroutine,避免阻塞托盘消息循环)
	go th.watchActions(onAction, a)
	return nil
}

// watchActions 消费托盘菜单回调。
func (th *trayHandle) watchActions(onAction chan trayMenu, a *App) {
	defer close(th.done)
	for {
		select {
		case <-th.stopCh:
			return
		case action := <-onAction:
			switch action {
			case trayShow:
				showMainWindow(a)
			case trayQuit:
				quitApplication(a)
			}
		}
	}
}

// stopTray 停止托盘图标(进程退出前调用)。
func (a *App) stopTray() {
	a.mu.Lock()
	th := a.tray
	a.tray = nil
	a.mu.Unlock()
	if th == nil {
		return
	}
	close(th.stopCh)
	if th.icon != nil {
		th.icon.Stop()
	}
	<-th.done
}
