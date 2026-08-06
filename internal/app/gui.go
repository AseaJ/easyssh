// Package app 提供桌面应用(GUI)与守护进程共用的应用层能力。
package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/asea/easyssh/internal/certmgr"
	"github.com/asea/easyssh/internal/config"
	"github.com/asea/easyssh/internal/logring"
	"github.com/asea/easyssh/internal/notify"
	"github.com/asea/easyssh/internal/scheduler"
	"github.com/asea/easyssh/internal/storage"
)

// App 是 Wails 前端可调用的应用对象。
// 前端通过 window.go.app.App.<Method> 访问导出方法。
type App struct {
	ctx        context.Context
	configDir  string     // 配置文件目录(配置文件名固定 easyssh.yaml)
	mu         sync.Mutex // 保护 cfg/rawCfg/sched/lastRun/lastScan/lastError/missingEnv(reload、自动扫描、Wails 方法并发访问)
	cfg        *config.Config
	rawCfg     *config.Config // 未展开 env 的原始配置(GetConfig 用它显示密钥引用,不暴露明文)
	runCfg     *config.Config // 运行时配置:本地相对路径已基于配置目录解析为绝对路径(内部逻辑用,展示/保存仍用 cfg)
	sched      *scheduler.Scheduler
	logger     *log.Logger
	logs       *logring.Ring // GUI 日志面板数据源
	missingEnv []string      // 配置中引用了但未设置的环境变量(提示用户补齐)
	lastRun    time.Time     // 最近一次扫描/操作时间(概览展示)
	lastScan   time.Time     // 自动调度循环最近一次实际执行扫描的时间(按 check_interval 触发)
	lastError  string
	tray       *trayHandle   // Windows 托盘图标(非 Windows 为 nil)
	quitting   bool          // 托盘"退出"标志:置 true 后 runtime.Quit 不被 OnBeforeClose 拦截
	autostartMode bool       // --autostart 启动:开机自启模式,窗口默认隐藏
}

// snap 返回当前配置与调度器的快照指针。reload 总是构造新对象后原子替换指针,
// 因此旧指针仍安全可用;配合 mu 保证读写的可见性。
func (a *App) snap() (cfg *config.Config, sched *scheduler.Scheduler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runCfg != nil {
		return a.runCfg, a.sched
	}
	return a.cfg, a.sched
}

// NewApp 创建应用对象。configDir 为配置文件所在目录。
func NewApp(configDir string) *App {
	logs := logring.New(1000)
	logger := log.New(logs, "", log.LstdFlags)
	return &App{configDir: configDir, logger: logger, logs: logs}
}

// SetAutostartMode 标记本次为开机自启模式(窗口默认隐藏,仅托盘后台)。
// 在 wails.Run 前调用。托盘初始化失败时自启模式会直接退出而非隐藏成无入口进程。
func (a *App) SetAutostartMode() {
	a.autostartMode = true
}

// LogWriter 返回日志写入器(供全局 log.SetOutput 使用,收集 lego 等三方日志)。
func (a *App) LogWriter() io.Writer {
	return a.logs
}

// Startup 由 Wails 生命周期调用:加载配置并初始化调度器,并启动自动周期扫描。
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	// 从注册表加载先前持久化的环境变量(密钥),保证重启后仍可用
	LoadPersistedEnv()
	if err := a.reload(); err != nil {
		a.lastError = fmt.Sprintf("配置加载失败(%s): %v", a.cfgPath(), err)
		a.logger.Printf("配置加载失败: %v", err)
	} else {
		a.logger.Printf("配置加载成功:%s", a.cfgPath())
		go a.autoLoop() // GUI 打开后按 check_interval 自动扫描(与 serve 模式一致)
	}
	// 启动托盘图标(失败仅记日志,不阻断应用;非 Windows 无托盘)
	trayErr := a.startTray()
	if trayErr != nil {
		a.logger.Printf("托盘初始化失败: %v", trayErr)
		// --autostart 自启模式下托盘是唯一交互入口,失败则退出,避免隐藏窗口后无法恢复
		if a.autostartMode {
			a.lastError = fmt.Sprintf("托盘初始化失败(自启模式),请手动打开应用: %v", trayErr)
			a.quitting = true // 置位放行 BeforeClose,确保 runtime.Quit 真正退出
			wailsQuit(a.ctx)
		}
	}
	// 缓存主窗口 hwnd(供托盘"打开界面/隐藏"切换任务栏按钮;StartHidden 时窗口未显示,延迟到首次显示)
	cacheMainWindow()
}

// autoLoop 自动调度循环:每 1 分钟检查一次是否到 check_interval,到点执行全量扫描。
// 用短 tick + 到期判断,使 check_interval 热更新后能即时生效。
func (a *App) autoLoop() {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-tick.C:
			a.runDueScan()
		}
	}
}

// runDueScan 检查并执行到期扫描。
func (a *App) runDueScan() {
	a.mu.Lock()
	cfg := a.cfg
	sched := a.sched
	interval := 6 * time.Hour
	if cfg != nil {
		interval = cfg.Schedule.CheckInterval.Std()
	}
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	due := a.lastScan.IsZero() || time.Since(a.lastScan) >= interval
	if due {
		a.lastScan = time.Now()
	}
	a.mu.Unlock()
	if cfg == nil || sched == nil || !due {
		return
	}
	reports := sched.RunOnce(a.ctx)
	a.mu.Lock()
	a.lastRun = time.Now()
	a.lastError = ""
	for _, r := range reports {
		if !r.OK {
			a.lastError = fmt.Sprintf("%s: %s", r.Name, r.Error)
		}
	}
	a.mu.Unlock()
	a.logger.Printf("自动扫描完成: %d 个条目", len(reports))
}

// reload 重新加载配置与调度器(配置热更新)。
// 使用宽容模式:缺失的环境变量(密钥)不阻塞加载,记入 missingEnv 供界面提示。
func (a *App) reload() error {
	cfg, missing, err := config.LoadAndExpandLenient(a.cfgPath())
	if err != nil {
		return err
	}
	raw, err := config.Load(a.cfgPath()) // 未展开的引用形式,供 GetConfig 展示密钥引用
	if err != nil {
		a.logger.Printf("读取原始配置失败(密钥引用将不可用): %v", err)
	}
	// 运行时配置:本地相对路径基于配置目录解析为绝对,
	// 保证无论从哪个工作目录启动,storage.dir / account_key / ssh key 都能定位到
	runCfg := config.Clone(cfg)
	config.ResolvePaths(runCfg, a.configDir)
	a.mu.Lock()
	a.cfg = cfg
	a.rawCfg = raw
	a.runCfg = runCfg
	a.missingEnv = missing
	a.sched = scheduler.New(runCfg, nil, a.logger)
	a.mu.Unlock()
	if len(missing) > 0 {
		a.logger.Printf("环境变量未设置(可在配置页密钥栏补齐): %v", missing)
	}
	return nil
}

func (a *App) cfgPath() string {
	if a.configDir == "" {
		return "easyssh.yaml"
	}
	return filepath.Join(a.configDir, "easyssh.yaml")
}

// GetLogs 返回最近的日志(供 GUI 日志面板)。
func (a *App) GetLogs(limit int) []logring.Entry {
	return a.logs.Entries(limit)
}

// --- 前端数据结构 ---

// CertView 是证书列表项的展示结构。
type CertView struct {
	Name        string   `json:"name"`
	Domains     []string `json:"domains"`
	NotAfter    string   `json:"not_after"`
	RemainDays  int      `json:"remain_days"`
	Status      string   `json:"status"` // 未签发/fresh/renewing/retry/expired
	Fingerprint string   `json:"fingerprint"`
	Issuer      string   `json:"issuer"`
	Deployed    bool     `json:"deployed"`
	LastError   string   `json:"last_error,omitempty"`
}

// Overview 是仪表盘概览。
type Overview struct {
	Total      int      `json:"total"`
	Healthy    int      `json:"healthy"`
	Expiring   int      `json:"expiring"`
	Failed     int      `json:"failed"`
	NotIssued  int      `json:"not_issued"`
	Schedule   string   `json:"schedule"`
	ConfigPath string   `json:"config_path"`
	LastRun    string   `json:"last_run"`
	LastError  string   `json:"last_error,omitempty"`
	MissingEnv []string `json:"missing_env,omitempty"`
	CA         string   `json:"ca"`
}

// --- Wails 绑定方法 ---

// ListCertificates 返回全部证书条目列表。
func (a *App) ListCertificates() ([]CertView, error) {
	cfg, _ := a.snap()
	if cfg == nil {
		return nil, fmt.Errorf("配置未加载")
	}
	views := make([]CertView, 0, len(cfg.Certificates))
	for i := range cfg.Certificates {
		entry := &cfg.Certificates[i]
		v := CertView{
			Name:    entry.Name,
			Domains: entry.Domains,
			Status:  "未签发",
		}
		f, err := storage.NewFS(entry.Storage.Dir)
		if err != nil {
			return nil, err
		}
		b, err := f.Load(a.ctx, entry.Name)
		if err != nil {
			return nil, err
		}
		if b != nil && len(b.LeafPEM) > 0 {
			v.NotAfter = b.NotAfter.Format("2006-01-02 15:04")
			v.RemainDays = int(time.Until(b.NotAfter).Hours() / 24)
			v.Fingerprint = b.Fingerprint
			v.Issuer = b.Issuer
			v.Deployed = b.Meta.DeployedFingerprint == b.Fingerprint
			v.LastError = b.Meta.LastError
			switch {
			case b.Meta.FailCount > 0:
				v.Status = "retry"
			case time.Until(b.NotAfter) <= 0:
				v.Status = "expired"
			case b.ExpiringIn(cfg.Schedule.RenewBefore.Std()):
				v.Status = "renewing"
			default:
				v.Status = "fresh"
			}
		}
		views = append(views, v)
	}
	return views, nil
}

// GetOverview 返回仪表盘概览。
func (a *App) GetOverview() (Overview, error) {
	views, err := a.ListCertificates()
	if err != nil {
		return Overview{}, err
	}
	a.mu.Lock()
	cfg := a.cfg
	sched := a.sched
	lastRun := a.lastRun
	lastError := a.lastError
	missingEnv := append([]string(nil), a.missingEnv...)
	a.mu.Unlock()
	schedule := "-"
	caServer := "-"
	if sched != nil {
		schedule = sched.Describe()
	}
	if cfg != nil {
		caServer = cfg.CA.Server
	}
	ov := Overview{
		Total:      len(views),
		Schedule:   schedule,
		ConfigPath: a.cfgPath(),
		LastRun:    lastRun.Format("2006-01-02 15:04:05"),
		LastError:  lastError,
		MissingEnv: missingEnv,
		CA:         caServer,
	}
	for _, v := range views {
		switch v.Status {
		case "fresh":
			ov.Healthy++
		case "renewing":
			ov.Expiring++
		case "retry", "expired":
			ov.Failed++
		default:
			ov.NotIssued++
		}
	}
	if lastRun.IsZero() {
		ov.LastRun = "-"
	}
	return ov, nil
}

// Renew 立即强制续期指定条目。
func (a *App) Renew(name string) (string, error) {
	_, sched := a.snap()
	if sched == nil {
		return "", fmt.Errorf("调度器未初始化")
	}
	rep, err := sched.ForceRenew(a.ctx, name)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.lastRun = time.Now()
	if !rep.OK {
		a.lastError = rep.Error
	} else {
		a.lastError = ""
	}
	a.mu.Unlock()
	if !rep.OK {
		return "", fmt.Errorf("%s", rep.Error)
	}
	return "续期成功:" + name, nil
}

// Deploy 对指定条目执行补部署。
func (a *App) Deploy(name string) (string, error) {
	_, sched := a.snap()
	if sched == nil {
		return "", fmt.Errorf("调度器未初始化")
	}
	rep, err := sched.ForceDeploy(a.ctx, name)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.lastRun = time.Now()
	if !rep.OK {
		a.lastError = rep.Error
	} else {
		a.lastError = ""
	}
	a.mu.Unlock()
	if !rep.OK {
		a.logger.Printf("[%s] 部署失败: %s", name, rep.Error)
		return "", fmt.Errorf("%s", rep.Error)
	}
	return "部署完成:" + name, nil
}

// RunOnce 手动触发一次全量扫描(供 CLI/serve 使用,会执行续期/部署)。
func (a *App) RunOnce() (string, error) {
	_, sched := a.snap()
	if sched == nil {
		return "", fmt.Errorf("调度器未初始化")
	}
	reports := sched.RunOnce(a.ctx)
	a.mu.Lock()
	a.lastRun = time.Now()
	a.lastError = ""
	failed := 0
	for _, r := range reports {
		if !r.OK {
			failed++
			a.lastError = fmt.Sprintf("%s: %s", r.Name, r.Error)
		}
	}
	a.mu.Unlock()
	if failed > 0 {
		return fmt.Sprintf("扫描完成,失败 %d 个条目", failed), nil
	}
	return "扫描完成,全部正常", nil
}

// RunCheck 手动执行一次纯检查扫描(供 GUI「立即扫描」按钮):只评估状态,
// 不续期、不部署。返回每个条目的检查摘要。
func (a *App) RunCheck() (string, error) {
	_, sched := a.snap()
	if sched == nil {
		return "", fmt.Errorf("调度器未初始化")
	}
	reports := sched.CheckOnly(a.ctx)
	a.mu.Lock()
	a.lastRun = time.Now()
	a.lastError = ""
	for _, r := range reports {
		if !r.OK {
			a.lastError = fmt.Sprintf("%s: %s", r.Name, r.Error)
		}
	}
	a.mu.Unlock()

	actionName := map[certmgr.Action]string{
		certmgr.ActionSkip:    "正常",
		certmgr.ActionRenew:   "待续期",
		certmgr.ActionReissue: "待重签",
		certmgr.ActionIssue:   "未签发",
		certmgr.ActionDeploy:  "待部署",
		certmgr.ActionRetry:   "异常",
	}
	lines := make([]string, 0, len(reports))
	bad := 0
	for _, r := range reports {
		name := actionName[r.Action]
		if name == "" {
			name = string(r.Action)
		}
		status := "✓"
		if !r.OK {
			status = "✗"
			bad++
		}
		lines = append(lines, fmt.Sprintf("%s %s[%s]", status, r.Name, name))
	}
	if len(lines) == 0 {
		a.logger.Printf("手动扫描:未配置证书条目")
		return "未配置证书条目", nil
	}
	head := fmt.Sprintf("检查完成:%d 个条目", len(reports))
	if bad > 0 {
		head += fmt.Sprintf(",%d 个异常", bad)
	}
	// 写入日志面板:总览 + 逐条结果(与自动扫描的日志风格一致)
	a.logger.Printf("手动扫描完成:%d 个条目,%d 个异常", len(reports), bad)
	for _, r := range reports {
		name := actionName[r.Action]
		if name == "" {
			name = string(r.Action)
		}
		if r.OK {
			a.logger.Printf("  ✓ %s[%s]", r.Name, name)
		} else {
			a.logger.Printf("  ✗ %s[%s]: %s", r.Name, name, r.Error)
		}
	}
	return head + " " + strings.Join(lines, " "), nil
}

// ReloadConfig 重新加载配置文件(热更新)。
func (a *App) ReloadConfig() (string, error) {
	if err := a.reload(); err != nil {
		a.mu.Lock()
		a.lastError = err.Error()
		a.mu.Unlock()
		return "", err
	}
	a.mu.Lock()
	a.lastError = ""
	a.mu.Unlock()
	return "配置已重新加载", nil
}

// TestNotify 发送一封固定内容的测试邮件(供「通知设置」页测试按钮)。
// 仅验证 SMTP 发送链路:成功表示邮件已发出,是否收到由用户自行确认;
// 失败返回具体错误(配置缺失 / TLS 连接 / 认证 / 收件人拒绝等)。
func (a *App) TestNotify() (string, error) {
	cfg, _ := a.snap()
	if cfg == nil {
		return "", fmt.Errorf("配置未加载")
	}
	smtpCfg := cfg.Notify.SMTP
	if smtpCfg.Host == "" || smtpCfg.User == "" || smtpCfg.Pass == "" {
		return "", fmt.Errorf("SMTP 配置不完整:请填写服务器、账号与授权码")
	}
	if len(smtpCfg.To) == 0 {
		return "", fmt.Errorf("未配置收件人")
	}
	if smtpCfg.Port == 0 {
		smtpCfg.Port = 465
	}
	msg := notify.BuildTestMail(smtpCfg.User, smtpCfg.To)
	if err := notify.SendSMTP(smtpCfg, msg); err != nil {
		return "", fmt.Errorf("测试邮件发送失败: %w", err)
	}
	a.logger.Printf("已发送测试邮件至 %v", smtpCfg.To)
	return fmt.Sprintf("测试邮件已发送至 %s,请查收确认", strings.Join(smtpCfg.To, ", ")), nil
}
