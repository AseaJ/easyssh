//go:build windows

package app

import "context"

// BeforeClose 由 Wails 生命周期调用(用户关窗或 runtime.Quit 时)。
// 返回 true 阻止进程退出。
// 规则:
//   - 托盘"退出"(quitting=true)→ 放行(返回 false);
//   - 用户关窗 → 隐藏到托盘:隐藏主窗口 + 移除任务栏按钮,返回 true 阻止退出。
func (a *App) BeforeClose(ctx context.Context) bool {
	a.mu.Lock()
	quitting := a.quitting
	a.mu.Unlock()
	if quitting {
		return false // 托盘"退出"放行
	}
	// 用户关窗:隐藏到托盘继续后台运行
	hideWindowToTray(a)
	return true
}

// OnShutdown 由 Wails 生命周期调用(进程即将退出)。
func (a *App) OnShutdown(ctx context.Context) {
	a.stopTray()
}

// HideToTray 供托盘菜单与前端调用:隐藏主窗口到托盘。
func (a *App) HideToTray() {
	hideWindowToTray(a)
}

// QuitFromTray 供托盘菜单调用:设置退出标志并真正退出。
func (a *App) QuitFromTray() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	wailsQuit(a.ctx)
}
