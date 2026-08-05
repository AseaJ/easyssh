// Package scheduler 实现续期调度:按配置周期扫描全部条目,
// 依据 certmgr 状态机决策并执行签发/续期,带退避与限流。
package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/asea/easyssh/internal/acme"
	"github.com/asea/easyssh/internal/certmgr"
	"github.com/asea/easyssh/internal/config"
	"github.com/asea/easyssh/internal/deploy"
	"github.com/asea/easyssh/internal/notify"
	"github.com/asea/easyssh/internal/storage"
)

// Report 是一次扫描中单个条目的执行结果。
type Report struct {
	Name   string
	Action certmgr.Action
	OK     bool
	Error  string
}

// ProvisionerFactory 按条目创建签发源(便于测试注入 mock)。
type ProvisionerFactory func(entry *config.CertificateConfig) (acme.Provisioner, error)

// DeployerFactory 按配置创建部署目标(便于测试注入 mock)。
type DeployerFactory func(cfg config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error)

// Scheduler 是证书生命周期调度器。
type Scheduler struct {
	cfg           *config.Config
	factory       ProvisionerFactory
	deployFactory DeployerFactory
	limiter       *rate.Limiter
	logger        *log.Logger
	notifiers     []notify.Notifier
	maxFails      int
	locks         sync.Map // name -> *sync.Mutex:单条目互斥,防止自动扫描与手动操作并发处理同一条目
}

func New(cfg *config.Config, factory ProvisionerFactory, logger *log.Logger) *Scheduler {
	if factory == nil {
		factory = func(entry *config.CertificateConfig) (acme.Provisioner, error) {
			return acme.New(acme.Options{
				Server:      cfg.CA.Server,
				Email:       cfg.CA.Email,
				AccountKey:  cfg.CA.AccountKey,
				Challenge:   entry.Challenge,
				DNSProvider: entry.DNSProvider,
				DNSOpts:     entry.DNSProviderOpts,
			})
		}
	}
	notifiers := []notify.Notifier{notify.NewLogger(logger)}
	if cfg.Notify.Webhook != "" {
		notifiers = append(notifiers, notify.NewWebhook(cfg.Notify.Webhook))
	}
	if e, err := notify.NewEmail(cfg.Notify.SMTP); err == nil {
		notifiers = append(notifiers, e)
	} else if cfg.Notify.SMTP.Host != "" {
		logger.Printf("邮件告警配置无效: %v", err)
	}
	return &Scheduler{
		cfg:     cfg,
		factory: factory,
		deployFactory: func(dc config.DeployConfig, hosts []config.HostConfig) (deploy.Deployer, error) {
			return deploy.NewDeployer(dc, hosts)
		},
		limiter:   rate.NewLimiter(rate.Every(2*time.Second), 1), // 尊重 CA 速率限制
		logger:    logger,
		notifiers: notifiers,
		maxFails:  10,
	}
}

// CheckOnly 执行一次纯检查扫描:只评估每个条目当前状态(未签发/正常/待续期/重试/过期),
// 不签发、不续期、不部署、不写任何 meta。供 GUI「立即扫描」按钮使用,避免误触发续期/部署。
func (s *Scheduler) CheckOnly(ctx context.Context) []Report {
	now := time.Now()
	reports := make([]Report, 0, len(s.cfg.Certificates))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := range s.cfg.Certificates {
		entry := &s.cfg.Certificates[i]
		wg.Add(1)
		go func(e *config.CertificateConfig) {
			defer wg.Done()
			unlock := s.lockEntry(e.Name)
			defer unlock()
			rep := s.checkEntry(ctx, e, now)
			mu.Lock()
			reports = append(reports, rep)
			mu.Unlock()
		}(entry)
	}
	wg.Wait()
	return reports
}

// checkEntry 评估单个条目当前状态并给出建议动作,但不执行任何签发/续期/部署。
func (s *Scheduler) checkEntry(ctx context.Context, entry *config.CertificateConfig, now time.Time) Report {
	store, err := storage.NewFS(entry.Storage.Dir)
	if err != nil {
		return Report{entry.Name, certmgr.ActionRetry, false, "读取存储失败: " + err.Error()}
	}
	bundle, err := store.Load(ctx, entry.Name)
	if err != nil {
		return Report{entry.Name, certmgr.ActionRetry, false, "读取证书状态失败: " + err.Error()}
	}

	dec := certmgr.Decide(entry.Domains, bundle, s.cfg.Schedule.RenewBefore.Std(), now)
	// 与 processEntry 保持一致:证书有效但未部署到全部目标时,建议动作为 deploy(但仅提示,不执行)
	if dec.Action == certmgr.ActionSkip && bundle != nil && bundle.Meta.FailCount == 0 && !allDeployed(bundle, entry) {
		return Report{entry.Name, certmgr.ActionDeploy, true, "证书正常但未部署到全部目标"}
	}
	if dec.Action == certmgr.ActionSkip {
		return Report{entry.Name, certmgr.ActionSkip, true, "证书正常,无需处理"}
	}
	if bundle != nil && bundle.Meta.FailCount >= s.maxFails {
		return Report{entry.Name, certmgr.ActionSkip, false, "连续失败超过上限,已停止自动重试"}
	}
	return Report{entry.Name, dec.Action, true, dec.Reason}
}

// lockEntry 获取指定条目的互斥锁,返回解锁函数。
func (s *Scheduler) lockEntry(name string) func() {
	v, _ := s.locks.LoadOrStore(name, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// RunOnce 执行一次全量扫描,返回每个条目的执行报告。
func (s *Scheduler) RunOnce(ctx context.Context) []Report {
	now := time.Now()
	reports := make([]Report, 0, len(s.cfg.Certificates))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := range s.cfg.Certificates {
		entry := &s.cfg.Certificates[i]
		wg.Add(1)
		go func(e *config.CertificateConfig) {
			defer wg.Done()
			unlock := s.lockEntry(e.Name)
			defer unlock()
			rep := s.processEntry(ctx, e, now)
			mu.Lock()
			reports = append(reports, rep)
			mu.Unlock()
		}(entry)
	}
	wg.Wait()
	return reports
}

func (s *Scheduler) processEntry(ctx context.Context, entry *config.CertificateConfig, now time.Time) Report {
	store, err := storage.NewFS(entry.Storage.Dir)
	if err != nil {
		return Report{entry.Name, certmgr.ActionRetry, false, err.Error()}
	}
	bundle, err := store.Load(ctx, entry.Name)
	if err != nil {
		return Report{entry.Name, certmgr.ActionRetry, false, err.Error()}
	}

	dec := certmgr.Decide(entry.Domains, bundle, s.cfg.Schedule.RenewBefore.Std(), now)
	if dec.Action == certmgr.ActionSkip {
		// 证书有效且未部署到全部目标时,触发补部署(退避中的条目除外)
		if bundle != nil && bundle.Meta.FailCount == 0 && !allDeployed(bundle, entry) {
			dec = certmgr.Decision{Action: certmgr.ActionDeploy, Reason: "证书已签发但未部署到全部目标"}
		} else {
			return Report{entry.Name, dec.Action, true, dec.Reason}
		}
	}

	// 达到失败上限后不再自动重试(等待人工介入/告警)
	if bundle != nil && bundle.Meta.FailCount >= s.maxFails {
		return Report{entry.Name, certmgr.ActionSkip, false, "连续失败超过上限,停止自动重试"}
	}

	s.logger.Printf("[%s] 动作=%s 原因=%s", entry.Name, dec.Action, dec.Reason)

	// 纯部署动作:证书已有效,直接部署,不重新签发
	if dec.Action == certmgr.ActionDeploy {
		if err := s.deployBundle(ctx, store, bundle, entry); err != nil {
			s.alert(ctx, notify.Event{
				Level: "warn", Subject: "证书部署失败", Message: err.Error(),
				Entry: entry.Name, Time: time.Now(),
			})
			return Report{entry.Name, dec.Action, false, "部署失败: " + err.Error()}
		}
		s.alert(ctx, notify.Event{
			Level: "info", Kind: "success", Subject: "证书补部署成功",
			Message: fmt.Sprintf("%s 已部署到全部目标,新到期时间: %s", entry.Name, bundle.NotAfter.Format("2006-01-02 15:04")),
			Entry:   entry.Name, Time: time.Now(),
		})
		return Report{entry.Name, dec.Action, true, ""}
	}

	// 即将到期提醒:进入续期窗口时推送一次(24h 去重,记录到 meta 防重启后重复)
	if dec.Action == certmgr.ActionRenew && bundle != nil {
		last := bundle.Meta.ExpiringNotifiedAt
		if last.IsZero() || time.Since(last) >= 24*time.Hour {
			s.alert(ctx, notify.Event{
				Level: "info", Kind: "expiring",
				Subject: "证书即将到期,已进入自动续期窗口",
				Message: fmt.Sprintf("%s 将于 %s 到期(剩余约 %d 天),系统将自动续期并部署",
					entry.Name, bundle.NotAfter.Format("2006-01-02 15:04"),
					int(time.Until(bundle.NotAfter).Hours()/24)),
				Entry: entry.Name, Time: time.Now(),
			})
			bundle.Meta.ExpiringNotifiedAt = time.Now()
			if err := store.UpdateMeta(ctx, &bundle.Meta); err != nil {
				s.logger.Printf("[%s] 记录到期提醒时间失败: %v", entry.Name, err)
			}
		}
	}

	if err := s.limiter.Wait(ctx); err != nil {
		return Report{entry.Name, dec.Action, false, "限流等待取消: " + err.Error()}
	}

	prov, err := s.factory(entry)
	if err != nil {
		s.recordFailure(store, bundle, entry, err)
		return Report{entry.Name, dec.Action, false, err.Error()}
	}

	newBundle, err := prov.Ensure(ctx, entry.Domains)
	if err != nil {
		s.recordFailure(store, bundle, entry, err)
		return Report{entry.Name, dec.Action, false, "签发失败: " + err.Error()}
	}

	// 成功:重置失败状态,保留已部署指纹(供部署层幂等判断)
	newBundle.Name = entry.Name
	newBundle.Meta.FailCount = 0
	newBundle.Meta.LastError = ""
	newBundle.Meta.NextRetryAt = time.Time{}
	if bundle != nil {
		newBundle.Meta.DeployedFingerprint = bundle.Meta.DeployedFingerprint
		newBundle.Meta.DeployedTargets = bundle.Meta.DeployedTargets
	}
	if err := store.Save(ctx, newBundle); err != nil {
		s.recordFailure(store, bundle, entry, err)
		return Report{entry.Name, dec.Action, false, "保存证书失败: " + err.Error()}
	}
	// 部署失败也告警
	if err := s.deployBundle(ctx, store, newBundle, entry); err != nil {
		s.alert(ctx, notify.Event{
			Level:   "warn",
			Subject: "证书部署失败",
			Message: err.Error(),
			Entry:   entry.Name,
			Time:    time.Now(),
		})
		return Report{entry.Name, dec.Action, false, "签发成功但部署失败: " + err.Error()}
	}
	s.logger.Printf("[%s] %s+部署成功,有效期至 %s", entry.Name, dec.Action, newBundle.NotAfter.Format(time.RFC3339))
	s.alert(ctx, notify.Event{
		Level: "info", Kind: "success",
		Subject: issueSubject(dec.Action),
		Message: fmt.Sprintf("%s 签发/续期完成,新到期时间: %s(签发者 %s)",
			entry.Name, newBundle.NotAfter.Format("2006-01-02 15:04"), newBundle.Issuer),
		Entry: entry.Name, Time: time.Now(),
	})
	return Report{entry.Name, dec.Action, true, ""}
}

// issueSubject 返回签发类动作的通知标题。
func issueSubject(a certmgr.Action) string {
	switch a {
	case certmgr.ActionRenew:
		return "证书续期成功"
	case certmgr.ActionReissue:
		return "证书重签成功"
	default:
		return "证书签发成功"
	}
}

// deployBundle 依次部署到所有配置目标,每成功一个即更新 meta。
func (s *Scheduler) deployBundle(ctx context.Context, store *storage.FS, bundle *certmgr.Bundle, entry *config.CertificateConfig) error {
	for i := range entry.Deploy {
		d, err := s.deployFactory(entry.Deploy[i], s.cfg.Hosts)
		if err != nil {
			return fmt.Errorf("创建部署目标 %s: %w", entry.Deploy[i].Type, err)
		}
		if err := d.Deploy(ctx, bundle); err != nil {
			return fmt.Errorf("%s: %w", d.Name(), err)
		}
		// 每成功一个目标即持久化,部分失败时可下次补部署
		bundle.Meta.DeployedFingerprint = bundle.Fingerprint
		bundle.Meta.DeployedTargets = appendUnique(bundle.Meta.DeployedTargets, d.Name())
		if err := store.UpdateMeta(ctx, &bundle.Meta); err != nil {
			return fmt.Errorf("更新部署状态: %w", err)
		}
		s.logger.Printf("[%s] 已部署到 %s", entry.Name, d.Name())
	}
	return nil
}

// allDeployed 判断证书是否已部署到全部配置目标。
func allDeployed(bundle *certmgr.Bundle, entry *config.CertificateConfig) bool {
	if bundle.Meta.DeployedFingerprint != bundle.Fingerprint {
		return false
	}
	expected := make(map[string]bool)
	for i := range entry.Deploy {
		expected[entry.Deploy[i].Type] = true
	}
	if len(expected) == 0 {
		return true // 无部署目标,视为已部署
	}
	for _, t := range bundle.Meta.DeployedTargets {
		if expected[t] {
			delete(expected, t)
		}
	}
	return len(expected) == 0
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// ForceRenew 强制重新签发指定条目(忽略到期判断),走签发+部署完整流程。
// 用于 CLI 的 renew 子命令。
func (s *Scheduler) ForceRenew(ctx context.Context, name string) (Report, error) {
	unlock := s.lockEntry(name)
	defer unlock()
	var entry *config.CertificateConfig
	for i := range s.cfg.Certificates {
		if s.cfg.Certificates[i].Name == name {
			entry = &s.cfg.Certificates[i]
			break
		}
	}
	if entry == nil {
		return Report{}, fmt.Errorf("配置中不存在条目 %q", name)
	}
	s.logger.Printf("[%s] 强制续期开始: 域名 %v,挑战 %s", name, entry.Domains, entry.Challenge)
	if err := s.limiter.Wait(ctx); err != nil {
		return Report{entry.Name, certmgr.ActionRenew, false, "限流等待取消: " + err.Error()}, nil
	}
	prov, err := s.factory(entry)
	if err != nil {
		return Report{entry.Name, certmgr.ActionRenew, false, err.Error()}, nil
	}
	store, err := storage.NewFS(entry.Storage.Dir)
	if err != nil {
		return Report{entry.Name, certmgr.ActionRenew, false, err.Error()}, nil
	}
	bundle, _ := store.Load(ctx, entry.Name)

	s.logger.Printf("[%s] 正在向 CA 申请签发...", name)
	newBundle, err := prov.Ensure(ctx, entry.Domains)
	if err != nil {
		s.recordFailure(store, bundle, entry, err)
		return Report{entry.Name, certmgr.ActionRenew, false, "签发失败: " + err.Error()}, nil
	}
	s.logger.Printf("[%s] 签发成功: 签发者=%s 有效期至 %s", name, newBundle.Issuer, newBundle.NotAfter.Format(time.RFC3339))
	newBundle.Name = entry.Name
	newBundle.Meta.FailCount = 0
	newBundle.Meta.LastError = ""
	newBundle.Meta.NextRetryAt = time.Time{}
	if bundle != nil {
		newBundle.Meta.DeployedFingerprint = bundle.Meta.DeployedFingerprint
		newBundle.Meta.DeployedTargets = bundle.Meta.DeployedTargets
	}
	if err := store.Save(ctx, newBundle); err != nil {
		s.recordFailure(store, bundle, entry, err)
		return Report{entry.Name, certmgr.ActionRenew, false, "保存证书失败: " + err.Error()}, nil
	}
	s.logger.Printf("[%s] 证书已保存,开始部署到 %d 个目标", entry.Name, len(entry.Deploy))
	if err := s.deployBundle(ctx, store, newBundle, entry); err != nil {
		return Report{entry.Name, certmgr.ActionRenew, false, "签发成功但部署失败: " + err.Error()}, nil
	}
	s.logger.Printf("[%s] 强制续期+部署成功,有效期至 %s", entry.Name, newBundle.NotAfter.Format(time.RFC3339))
	s.alert(ctx, notify.Event{
		Level: "info", Kind: "success", Subject: "证书续期成功(手动)",
		Message: fmt.Sprintf("%s 新到期时间: %s(签发者 %s)",
			entry.Name, newBundle.NotAfter.Format("2006-01-02 15:04"), newBundle.Issuer),
		Entry: entry.Name, Time: time.Now(),
	})
	return Report{entry.Name, certmgr.ActionRenew, true, ""}, nil
}

// ForceDeploy 对指定条目执行补部署(证书有效但未部署到全部目标时)。
func (s *Scheduler) ForceDeploy(ctx context.Context, name string) (Report, error) {
	unlock := s.lockEntry(name)
	defer unlock()
	var entry *config.CertificateConfig
	for i := range s.cfg.Certificates {
		if s.cfg.Certificates[i].Name == name {
			entry = &s.cfg.Certificates[i]
			break
		}
	}
	if entry == nil {
		return Report{}, fmt.Errorf("配置中不存在条目 %q", name)
	}
	store, err := storage.NewFS(entry.Storage.Dir)
	if err != nil {
		return Report{}, err
	}
	bundle, err := store.Load(ctx, entry.Name)
	if err != nil {
		return Report{}, err
	}
	if bundle == nil || len(bundle.LeafPEM) == 0 {
		return Report{entry.Name, certmgr.ActionDeploy, false, "条目尚未签发,无法部署"}, nil
	}
	// 强制重新部署:清除已部署标记,让 Deployer 真正执行上传与校验
	bundle.Meta.DeployedFingerprint = ""
	bundle.Meta.DeployedTargets = nil
	if err := s.deployBundle(ctx, store, bundle, entry); err != nil {
		s.logger.Printf("[%s] 部署失败: %v", name, err)
		return Report{entry.Name, certmgr.ActionDeploy, false, "部署失败: " + err.Error()}, nil
	}
	s.logger.Printf("[%s] 补部署完成", entry.Name)
	s.alert(ctx, notify.Event{
		Level: "info", Kind: "success", Subject: "证书部署完成(手动)",
		Message: fmt.Sprintf("%s 已部署到全部目标,新到期时间: %s", entry.Name, bundle.NotAfter.Format("2006-01-02 15:04")),
		Entry:   entry.Name, Time: time.Now(),
	})
	return Report{entry.Name, certmgr.ActionDeploy, true, ""}, nil
}

// recordFailure 更新退避状态:失败次数 +1,下次重试时间按退避序列计算。
func (s *Scheduler) recordFailure(store *storage.FS, bundle *certmgr.Bundle, entry *config.CertificateConfig, cause error) {
	meta := &certmgr.Meta{
		Name:    entry.Name,
		Domains: entry.Domains,
	}
	if bundle != nil {
		meta = &bundle.Meta
	}
	meta.FailCount++
	meta.LastError = cause.Error()
	meta.NextRetryAt = time.Now().Add(certmgr.NextRetryAfter(meta.FailCount, s.backoffs()))
	if err := store.UpdateMeta(context.Background(), meta); err != nil {
		s.logger.Printf("[%s] 更新失败状态出错: %v", entry.Name, err)
	}
	s.logger.Printf("[%s] 失败(第 %d 次),下次重试 %s", entry.Name, meta.FailCount, meta.NextRetryAt.Format(time.RFC3339))
	// 告警:每次失败都通知,并在达到上限时给出更高级别提示
	s.alert(context.Background(), notify.Event{
		Level:   "error",
		Subject: "证书签发/续期失败",
		Message: cause.Error(),
		Entry:   entry.Name,
		Time:    time.Now(),
	})
	if meta.FailCount >= s.maxFails {
		s.alert(context.Background(), notify.Event{
			Level:   "error",
			Subject: "证书条目连续失败超过上限,已停止自动重试",
			Message: fmt.Sprintf("连续失败 %d 次,最近错误: %s", meta.FailCount, meta.LastError),
			Entry:   entry.Name,
			Time:    time.Now(),
		})
	}
}

// alert 向全部通知器发送事件。按 Events 开关过滤可选事件(失败通知始终发送)。
func (s *Scheduler) alert(ctx context.Context, e notify.Event) {
	switch e.Kind {
	case "expiring":
		if !s.cfg.Notify.Events.Expiring {
			return
		}
	case "success":
		if !s.cfg.Notify.Events.Success {
			return
		}
	}
	for _, n := range s.notifiers {
		if err := n.Notify(ctx, e); err != nil {
			s.logger.Printf("[notify] 发送通知失败: %v", err)
		}
	}
}

func (s *Scheduler) backoffs() []time.Duration {
	out := make([]time.Duration, len(s.cfg.Schedule.RetryBackoff))
	for i, d := range s.cfg.Schedule.RetryBackoff {
		out[i] = d.Std()
	}
	return out
}

// Describe 输出调度配置摘要(供 CLI/GUI 展示)。
func (s *Scheduler) Describe() string {
	return fmt.Sprintf("每 %s 扫描,剩余 %s 触发续期,退避 %v",
		s.cfg.Schedule.CheckInterval.Std(), s.cfg.Schedule.RenewBefore.Std(), s.backoffs())
}
