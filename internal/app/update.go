package app

import (
	"context"
	"sync"
	"time"

	"github.com/asea/easyssh/internal/update"
)

// UpdateInfo 是前端展示的更新信息(Wails 绑定方法返回值)。
type UpdateInfo struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	ReleaseURL  string `json:"release_url"`
	HasUpdate   bool   `json:"has_update"`
	CheckDone   bool   `json:"check_done"`   // 是否已完成一次检查(区分"正在检查"与"未检出")
	Error       string `json:"error,omitempty"` // 检查失败原因(如无网络),非致命
	CheckedAt   string `json:"checked_at,omitempty"`
}

// updateState 保存最近一次更新检测结果(供前端轮询读取)。
// 检测本身在后台 goroutine 进行,不阻塞 GUI 启动。
type updateState struct {
	mu     sync.Mutex
	info   UpdateInfo
}

// checkUpdate 后台检查新版本并缓存结果(幂等:已检查过且较新则跳过)。
// interval 为两次检查的最小间隔,避免频繁请求 GitHub API。
func (a *App) checkUpdate(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	a.updateMu.Lock()
	// 距上次检查不足 interval 则跳过
	if !a.updateCheckedAt.IsZero() && time.Since(a.updateCheckedAt) < interval {
		a.updateMu.Unlock()
		return
	}
	a.updateCheckedAt = time.Now()
	// 标记正在检查
	a.updateState.mu.Lock()
	a.updateState.info.CheckDone = false
	a.updateState.mu.Unlock()
	a.updateMu.Unlock()

	go func() {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		info, err := update.Latest(cctx, AppVersion())
		st := UpdateInfo{
			CheckDone: true,
			CheckedAt: time.Now().Format("2006-01-02 15:04"),
		}
		if err != nil {
			st.Error = err.Error()
			// 保留上次成功的 latest 值(若有),便于已显示过提示的会话持续提示
		} else {
			st.Current = info.Current
			st.Latest = info.Latest
			st.ReleaseURL = info.ReleaseURL
			st.HasUpdate = info.HasUpdate
		}
		a.updateState.mu.Lock()
		if st.Error == "" {
			// 成功:直接覆盖
			a.updateState.info = st
		} else if a.updateState.info.HasUpdate {
			// 失败但之前已检出更新:保留旧提示(清空错误,避免状态语义混乱),避免闪没
			st.HasUpdate = true
			st.Latest = a.updateState.info.Latest
			st.ReleaseURL = a.updateState.info.ReleaseURL
			st.Current = a.updateState.info.Current
			st.Error = ""
			a.updateState.info = st
		} else {
			// 失败且之前无更新:记录错误(供前端展示),不提示更新
			a.updateState.info = st
		}
		a.updateState.mu.Unlock()
		if err != nil {
			// 检测失败:重置节流时间,允许前端下次轮询立即重试,而不是等满 interval
			a.updateMu.Lock()
			a.updateCheckedAt = time.Time{}
			a.updateMu.Unlock()
		}
	}()
}

// CheckForUpdate 供前端轮询:返回最近一次更新检测结果。
// 前端启动时先调用一次以触发后台检测,再定时轮询。
func (a *App) CheckForUpdate() UpdateInfo {
	a.updateState.mu.Lock()
	defer a.updateState.mu.Unlock()
	return a.updateState.info
}

// OpenUpdatePage 打开最新版本发布页(用户点击提示条时调用)。
func (a *App) OpenUpdatePage() {
	info := a.CheckForUpdate()
	url := info.ReleaseURL
	if url == "" {
		url = update.ReleasesURL
	}
	a.logger.Printf("打开更新页面: %s", url)
	go openBrowser(url)
}
