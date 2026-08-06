//go:build windows

package app

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 托盘图标:用 Shell_NotifyIcon 在系统托盘(NOTIFICATION AREA)显示图标,
// 右击弹出菜单(打开界面 / 退出)。Wails v2.13 无内置托盘支持,故用纯 Win32 实现。
// 设计要点:
//   - 创建独立的隐藏窗口(专用 hwnd)承载托盘图标与菜单消息,不干扰 Wails 主窗口消息循环;
//   - 窗口与消息循环运行在绑定了 OS 线程的 goroutine(LockOSThread),消息队列是线程级的;
//   - 图标从当前 exe 资源加载(IDI_APPLICATION 兜底),无需额外图标文件;
//   - 菜单动作通过 channel 回传调用方(打开窗口 / 退出)。

var (
	shell32 = syscall.NewLazyDLL("shell32.dll")
	shellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")

	user32Tray = syscall.NewLazyDLL("user32.dll")
	createWindowExW   = user32Tray.NewProc("CreateWindowExW")
	defWindowProc     = user32Tray.NewProc("DefWindowProcW")
	destroyWindow     = user32Tray.NewProc("DestroyWindow")
	registerClassExW  = user32Tray.NewProc("RegisterClassExW")
	createPopupMenu   = user32Tray.NewProc("CreatePopupMenu")
	appendMenu        = user32Tray.NewProc("AppendMenuW")
	trackPopupMenu    = user32Tray.NewProc("TrackPopupMenu")
	destroyMenu       = user32Tray.NewProc("DestroyMenu")
	setForegroundWindow = user32Tray.NewProc("SetForegroundWindow")
	getCursorPos      = user32Tray.NewProc("GetCursorPos")
	peekMessage       = user32Tray.NewProc("PeekMessageW")
	translateMessage  = user32Tray.NewProc("TranslateMessage")
	dispatchMessage   = user32Tray.NewProc("DispatchMessageW")
	postQuitMessage   = user32Tray.NewProc("PostQuitMessage")
	loadIcon          = user32Tray.NewProc("LoadIconW")
)

// 常量(WinUser.h)
const (
	wsOverlapped   = 0x00000000
	wsExToolWindow = 0x00000080 // 不在任务栏/Alt-Tab 显示

	wmDestroy  = 0x0002
	wmUser     = 0x0400
	wmTrayIcon = wmUser + 0x20 // 托盘回调消息(自定义)

	// 托盘图标消息(Shell_NotifyIcon 回调 lParam 值)
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205

	nimAdd    = 0x00000000
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	// 菜单项命令 ID
	cmdShow = 1001
	cmdQuit = 1002

	// TrackPopupMenu 标志
	tpmLeftAlign   = 0x0000
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	// 消息泵
	pmRemove = 0x0001
	wmQuit   = 0x0012

	// 窗口类样式
	csDblClks = 0x0008

	// Wails 生成的 exe 资源中主图标资源 ID
	idAppIcon = 3

	// 系统默认图标 ID
	idiApplication = 32512
)

// notifyIconData 对应 Windows NOTIFYICONDATAW(部分字段,仅用到的)。
type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
}

// msg 对应 Win32 MSG。
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type point struct {
	x, y int32
}

// wndClassEx 对应 WNDCLASSEXW。
type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// trayMenu 是托盘菜单选择结果。
type trayMenu int

const (
	trayNone trayMenu = iota
	trayShow
	trayQuit
)

// trayIcon 管理 Windows 托盘图标。
type trayIcon struct {
	hwnd     uintptr
	mu       sync.Mutex
	quit     chan struct{}
	done     chan struct{}
	ready    chan struct{} // 窗口创建完成信号(成功/失败都会 close)
	initErr  error         // 初始化失败原因
	onAction chan trayMenu // 回调:菜单被选择(缓冲 1,非阻塞)
	visible  bool
}

// newTrayIcon 创建托盘图标并启动消息循环。
func newTrayIcon(onAction chan trayMenu) (*trayIcon, error) {
	t := &trayIcon{
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
		ready:    make(chan struct{}),
		onAction: onAction,
	}
	go t.run()
	// 等待窗口创建完成(最多 5 秒)
	select {
	case <-t.ready:
	case <-time.After(5 * time.Second):
		// 超时:后台 run 仍可能存活,显式清理避免消息循环线程泄漏
		t.Stop()
		return nil, fmt.Errorf("托盘初始化超时")
	}
	if t.initErr != nil {
		// initErr 已设置,run 会自行退出;但 stop 通道未关闭,主动 Stop 收敛
		t.Stop()
		return nil, t.initErr
	}
	return t, nil
}

// run 在绑定 OS 线程的 goroutine 中创建窗口、添加图标并运行消息循环。
// 窗口与消息循环必须同线程(消息队列是线程级的)。
func (t *trayIcon) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)

	hwnd, err := t.createHiddenWindow()
	if err != nil {
		t.initErr = err
		close(t.ready)
		return
	}
	t.hwnd = hwnd
	if err := t.addIcon(); err != nil {
		t.initErr = err
		destroyWindow.Call(hwnd)
		close(t.ready)
		return
	}
	close(t.ready)

	// 消息循环
	for {
		select {
		case <-t.quit:
			// 退出信号:销毁窗口(触发 wmDestroy → PostQuitMessage)后退出
			destroyWindow.Call(t.hwnd)
			return
		default:
		}
		var m msg
		r, _, _ := peekMessage.Call(uintptr(unsafe.Pointer(&m)), t.hwnd, 0, 0, pmRemove)
		if r != 0 {
			if m.message == wmQuit {
				return
			}
			translateMessage.Call(uintptr(unsafe.Pointer(&m)))
			dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}

// createHiddenWindow 创建独立隐藏窗口(不显示、不进任务栏),用于接收托盘消息。
func (t *trayIcon) createHiddenWindow() (uintptr, error) {
	className, _ := syscall.UTF16PtrFromString("easysshTrayWindow")
	hInst := currentModuleHandle()
	wndProc := syscall.NewCallback(func(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
		return t.wndProc(hwnd, m, wp, lp)
	})
	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		style:         csDblClks,
		lpfnWndProc:   wndProc,
		hInstance:     hInst,
		lpszClassName: className,
	}
	registerClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := createWindowExW.Call(
		uintptr(wsExToolWindow),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		uintptr(wsOverlapped),
		0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("创建托盘隐藏窗口失败")
	}
	return hwnd, nil
}

// wndProc 处理隐藏窗口消息。
func (t *trayIcon) wndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	switch m {
	case wmTrayIcon:
		return t.handleTrayMessage(lp)
	case wmDestroy:
		postQuitMessage.Call(0)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wp, lp)
	return r
}

// handleTrayMessage 处理托盘图标的鼠标/菜单回调。
func (t *trayIcon) handleTrayMessage(lparam uintptr) uintptr {
	switch lparam {
	case wmRButtonUp:
		t.showMenu()
	case wmLButtonUp:
		// 左键单击:打开界面(与菜单"打开界面"一致)
		select {
		case t.onAction <- trayShow:
		default:
		}
	}
	return 0
}

// showMenu 显示右键弹出菜单。
func (t *trayIcon) showMenu() {
	// 先置前窗口,否则 TrackPopupMenu 可能不消失(标准做法)
	setForegroundWindow.Call(t.hwnd)
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	showText, _ := syscall.UTF16PtrFromString("打开界面")
	quitText, _ := syscall.UTF16PtrFromString("退出")
	appendMenu.Call(menu, 0, cmdShow, uintptr(unsafe.Pointer(showText)))
	appendMenu.Call(menu, 0, cmdQuit, uintptr(unsafe.Pointer(quitText)))
	var pt point
	getCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// TrackPopupMenu 返回所选命令 ID(TPM_RETURNCMD)
	ret, _, _ := trackPopupMenu.Call(menu, uintptr(tpmLeftAlign|tpmRightButton|tpmReturnCmd),
		uintptr(pt.x), uintptr(pt.y), 0, t.hwnd, 0)
	switch ret {
	case cmdShow:
		select {
		case t.onAction <- trayShow:
		default:
		}
	case cmdQuit:
		select {
		case t.onAction <- trayQuit:
		default:
		}
	}
}

// addIcon 添加托盘图标。
func (t *trayIcon) addIcon() error {
	nid := t.buildNID(nifMessage | nifIcon | nifTip)
	r, _, _ := shellNotifyIcon.Call(uintptr(nimAdd), uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		return fmt.Errorf("Shell_NotifyIcon 添加图标失败")
	}
	t.mu.Lock()
	t.visible = true
	t.mu.Unlock()
	return nil
}

// buildNID 构造 NOTIFYICONDATA。
func (t *trayIcon) buildNID(flags uint32) notifyIconData {
	icon := t.loadAppIcon()
	tip, _ := syscall.UTF16FromString("easyssh 证书托管")
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = t.hwnd
	nid.uID = 1
	nid.uFlags = flags
	nid.uCallbackMessage = wmTrayIcon
	nid.hIcon = icon
	copy(nid.szTip[:], tip)
	return nid
}

// loadAppIcon 从当前 exe 资源加载应用图标(与窗口图标一致),失败时用系统默认图标兜底。
func (t *trayIcon) loadAppIcon() uintptr {
	// 从当前 exe 资源加载 ID 3 的主图标(与窗口一致)
	icon, _, _ := loadIcon.Call(currentModuleHandle(), uintptr(idAppIcon))
	if icon != 0 {
		return icon
	}
	// 兜底:系统默认应用图标
	icon, _, _ = loadIcon.Call(0, uintptr(idiApplication))
	return icon
}

// currentModuleHandle 返回当前 exe 的模块句柄(HINSTANCE)。
func currentModuleHandle() uintptr {
	var h windows.Handle
	// GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT(0x2):不增加引用计数,防止句柄泄漏
	if err := windows.GetModuleHandleEx(0x00000002, nil, &h); err == nil && h != 0 {
		return uintptr(h)
	}
	// 兜底:从本函数地址获取所属模块
	if err := windows.GetModuleHandleEx(0x00000004|0x00000002, nil, &h); err == nil && h != 0 {
		return uintptr(h)
	}
	return 0
}

// Stop 销毁托盘图标并停止消息循环。
func (t *trayIcon) Stop() {
	t.mu.Lock()
	if t.visible {
		nid := t.buildNID(nifMessage)
		shellNotifyIcon.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&nid)))
		t.visible = false
	}
	t.mu.Unlock()
	select {
	case <-t.quit:
	default:
		close(t.quit)
	}
	<-t.done
}

// traySupported 平台是否支持托盘。
func traySupported() bool { return true }

// --- Wails runtime 桥接 ---
// app 包不直接 import Wails runtime(避免与 main 的绑定耦合),改为由 main 注入。
// 非 Windows stub 为空实现;Windows 由 main 包在启动时注入真实调用。
var (
	wailsWindowShow = func(ctx context.Context) {}
	wailsWindowHide = func(ctx context.Context) {}
	wailsQuit       = func(ctx context.Context) {}
)

// InjectWailsHooks 由 main 包在 wails.Run 前调用,注入 Wails runtime 的实际调用。
// 平台无关(所有平台都定义);非 Windows 下托盘未启用,注入值不影响。
func InjectWailsHooks(show, hide, quit func(ctx context.Context)) {
	wailsWindowShow = show
	wailsWindowHide = hide
	wailsQuit = quit
}

// showMainWindow 显示主窗口并恢复任务栏按钮。
// 先 WindowShow 使窗口可见,再 restoreTaskbarButton 枚举(自启 StartHidden 时 hwnd 未缓存,需窗口可见才能枚举到)。
func showMainWindow(a *App) {
	wailsWindowShow(a.ctx)
	restoreTaskbarButton()
}

// quitApplication 真正退出应用(Wails runtime.Quit 会走 OnBeforeClose 与 Shutdown)。
func quitApplication(a *App) {
	wailsQuit(a.ctx)
}

// hideWindowToTray 隐藏主窗口到托盘:先找 hwnd(可见时)→ 移除任务栏按钮 → 隐藏窗口。
// 顺序关键:必须在窗口隐藏前找到 hwnd 并加 TOOLWINDOW 样式。
func hideWindowToTray(a *App) {
	hideTaskbarButton() // 先加 TOOLWINDOW(需窗口可见时找 hwnd;Startup 已缓存或此处重试)
	wailsWindowHide(a.ctx)
}

// restoreTaskbarButton/hideTaskbarButton 切换主窗口 WS_EX_TOOLWINDOW 样式,
// 控制任务栏按钮显隐。主窗口 hwnd 在 Startup 时缓存(见 cacheMainWindow),
// 避免每次 EnumWindows 枚举(跨线程枚举有已销毁窗口的竞态风险)。

var (
	user32Taskbar = syscall.NewLazyDLL("user32.dll")
	enumWindows    = user32Taskbar.NewProc("EnumWindows")
	getWindowThreadProcessId = user32Taskbar.NewProc("GetWindowThreadProcessId")
	setWindowLongPtr = user32Taskbar.NewProc("SetWindowLongPtrW")
	getWindowLongPtr = user32Taskbar.NewProc("GetWindowLongPtrW")
	isWindowVisible  = user32Taskbar.NewProc("IsWindowVisible")
)

const wsExToolwindow2 = 0x00000080

// GWL_EXSTYLE 是 GetWindowLongPtr/SetWindowLongPtr 的索引(负值,必须转成运行时 int)。
var gwlExstyle int = -20

// 主窗口 hwnd 缓存(进程级,Startup 时填充)。
var mainWindowHWND uintptr

// cacheMainWindow 在 Wails 主窗口可见时缓存其 hwnd。
// 在 Startup 里(窗口已创建但尚未显示)调用:枚举本进程可见顶层窗口。
// 仅调用一次;失败返回 0(后续 hide/restore 退化为无操作)。
func cacheMainWindow() uintptr {
	if mainWindowHWND != 0 {
		return mainWindowHWND
	}
	mainWindowHWND = findMainWindowOnce()
	return mainWindowHWND
}

// findMainWindowOnce 返回本进程内第一个可见的顶层窗口句柄(Wails 主窗口)。
func findMainWindowOnce() uintptr {
	pid := uint32(windows.GetCurrentProcessId())
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		var wpid uint32
		getWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		if wpid != pid {
			return 1 // 继续枚举
		}
		vis, _, _ := isWindowVisible.Call(hwnd)
		if vis != 0 {
			found = hwnd
			return 0 // 停止
		}
		return 1
	})
	enumWindows.Call(cb, 0)
	return found
}

// hideTaskbarButton 移除主窗口任务栏按钮(隐藏到托盘时)。
// hwnd 可能尚未缓存(自启 StartHidden 时 Startup 取不到),此处重试。
func hideTaskbarButton() {
	if mainWindowHWND == 0 {
		cacheMainWindow()
	}
	hwnd := mainWindowHWND
	if hwnd == 0 {
		return
	}
	index := uintptr(gwlExstyle)
	style, _, _ := getWindowLongPtr.Call(hwnd, index)
	setWindowLongPtr.Call(hwnd, index, style|uintptr(wsExToolwindow2))
}

// restoreTaskbarButton 恢复主窗口任务栏按钮(从托盘打开界面时)。
// hwnd 可能尚未缓存(自启 StartHidden 时 Startup 取不到),此处重试。
func restoreTaskbarButton() {
	if mainWindowHWND == 0 {
		cacheMainWindow()
	}
	hwnd := mainWindowHWND
	if hwnd == 0 {
		return
	}
	index := uintptr(gwlExstyle)
	style, _, _ := getWindowLongPtr.Call(hwnd, index)
	setWindowLongPtr.Call(hwnd, index, style&^uintptr(wsExToolwindow2))
}
