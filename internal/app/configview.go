package app

import (
	"crypto/sha1"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/asea/easyssh/internal/config"
)

// ConfigView 是前端可编辑的配置结构(表单数据)。
type ConfigView struct {
	CAServer       string         `json:"ca_server"`
	CAEmail        string         `json:"ca_email"`
	AccountKey     string         `json:"account_key"`
	CheckInterval  string         `json:"check_interval"`
	RenewBefore    string         `json:"renew_before"`
	RetryBackoff   []string       `json:"retry_backoff"`
	Webhook        string         `json:"webhook"`
	SMTPHost       string         `json:"smtp_host,omitempty"`
	SMTPPort       int            `json:"smtp_port,omitempty"`
	SMTPUser       string         `json:"smtp_user,omitempty"`
	SMTPPass       string         `json:"smtp_pass,omitempty"` // 授权码,{{env:VAR}} 引用或明文
	SMTPTo         []string       `json:"smtp_to,omitempty"`   // 收件人,可多个
	NotifyExpiring bool           `json:"notify_expiring"`     // 即将到期提醒开关
	NotifySuccess  bool           `json:"notify_success"`      // 续期/部署成功提醒开关
	Autostart      bool           `json:"autostart"`           // 开机自启(用户级 HKCU Run 键,非 yaml 配置项)
	Hosts          []HostEditView `json:"hosts,omitempty"`
	Certificates   []CertEditView `json:"certificates"`
}

// HostEditView 是可复用 SSH 目标的编辑视图。
type HostEditView struct {
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port,omitempty"`
	User         string `json:"user"`
	Key          string `json:"key,omitempty"`
	RemotePath   string `json:"remote_path,omitempty"`
	ReloadCmd    string `json:"reload_cmd,omitempty"`
	CertFilename string `json:"cert_filename,omitempty"`
	KeyFilename  string `json:"key_filename,omitempty"`
}

type CertEditView struct {
	Name        string            `json:"name"`
	Domains     []string          `json:"domains"`
	Challenge   string            `json:"challenge"`
	DNSProvider string            `json:"dns_provider"`
	DNSOpts     map[string]string `json:"dns_opts"` // 密钥值:{{env:VAR}} 引用或明文(保存时自动转 env)
	StorageDir  string            `json:"storage_dir"`
	Deploys     []DeployEditView  `json:"deploys"`
}

type DeployEditView struct {
	Type         string `json:"type"`
	ReloadCmd    string `json:"reload_cmd,omitempty"`
	TestCmd      string `json:"test_cmd,omitempty"`
	CertPath     string `json:"cert_path,omitempty"`
	KeyPath      string `json:"key_path,omitempty"`
	Host         string `json:"host,omitempty"`
	HostRef      string `json:"host_ref,omitempty"` // 引用 hosts 定义
	Port         int    `json:"port,omitempty"`
	User         string `json:"user,omitempty"`
	SSHKey       string `json:"ssh_key,omitempty"`
	RemotePath   string `json:"remote_path,omitempty"`
	CertFilename string `json:"cert_filename,omitempty"` // ssh:覆盖 host 的远程证书文件名
	KeyFilename  string `json:"key_filename,omitempty"`  // ssh:覆盖 host 的远程私钥文件名
	Dir          string `json:"dir,omitempty"`
	URL          string `json:"url,omitempty"`
}

// dnsNonSecretOpts 是 dns_provider_opts 中的非敏感参数。
// 这些参数不是凭证,直接以明文写入配置文件,不做 {{env:VAR}} 转换,
// 便于使用者直接查看和修改(如轮询间隔、传播超时)。比较时统一转大写。
var dnsNonSecretOpts = map[string]bool{
	"DNSPOD_POLLING_INTERVAL":    true,
	"DNSPOD_PROPAGATION_TIMEOUT": true,
}

// dnsNonSecretOpt 判断某个 dns_provider_opts 键是否为非敏感参数。
func dnsNonSecretOpt(key string) bool {
	return dnsNonSecretOpts[strings.ToUpper(strings.TrimSpace(key))]
}

// GetConfig 返回当前配置的可编辑视图(密钥显示 {{env:VAR}} 引用,不返回明文)。
func (a *App) GetConfig() (ConfigView, error) {
	a.mu.Lock()
	c := a.cfg
	raw := a.rawCfg
	a.mu.Unlock()
	if c == nil {
		return ConfigView{}, fmt.Errorf("配置未加载")
	}
	view := ConfigView{
		CAServer:       c.CA.Server,
		CAEmail:        c.CA.Email,
		AccountKey:     c.CA.AccountKey,
		CheckInterval:  durString(c.Schedule.CheckInterval.Std()),
		RenewBefore:    durString(c.Schedule.RenewBefore.Std()),
		Webhook:        c.Notify.Webhook,
		SMTPHost:       c.Notify.SMTP.Host,
		SMTPPort:       c.Notify.SMTP.Port,
		SMTPUser:       c.Notify.SMTP.User,
		SMTPTo:         []string(c.Notify.SMTP.To),
		NotifyExpiring: c.Notify.Events.Expiring,
		NotifySuccess:  c.Notify.Events.Success,
		Autostart:      AutostartEnabled(),
	}
	for _, d := range c.Schedule.RetryBackoff {
		view.RetryBackoff = append(view.RetryBackoff, durString(d.Std()))
	}
	for i := range c.Hosts {
		h := &c.Hosts[i]
		view.Hosts = append(view.Hosts, HostEditView{
			Name: h.Name, Host: h.Host, Port: h.Port, User: h.User,
			Key: h.Key, RemotePath: h.RemotePath, ReloadCmd: h.ReloadCmd,
			CertFilename: h.CertFilename, KeyFilename: h.KeyFilename,
		})
	}
	// SMTP 密码与 DNS 密钥取原始配置(引用形式),不暴露展开后的明文
	if raw != nil {
		view.SMTPPass = raw.Notify.SMTP.Pass
	}
	for i := range c.Certificates {
		cc := &c.Certificates[i]
		cv := CertEditView{
			Name:        cc.Name,
			Domains:     cc.Domains,
			Challenge:   cc.Challenge,
			DNSProvider: cc.DNSProvider,
			StorageDir:  cc.Storage.Dir,
		}
		// 密钥取原始配置(引用形式),不暴露展开后的明文
		if raw != nil {
			for j := range raw.Certificates {
				if raw.Certificates[j].Name == cc.Name {
					cv.DNSOpts = maps.Clone(raw.Certificates[j].DNSProviderOpts)
					// 非敏感参数(轮询间隔/传播超时)直接以明文展示,便于查看与修改;
					// 若旧配置误存为 env 引用,则展开为当前环境变量值
					for k, v := range cv.DNSOpts {
						if dnsNonSecretOpt(k) && isEnvRef(v) {
							if real, ok := os.LookupEnv(envRefName(v)); ok {
								cv.DNSOpts[k] = real
							}
						}
					}
					break
				}
			}
		}
		if cv.DNSOpts == nil {
			cv.DNSOpts = map[string]string{}
		}
		for j := range cc.Deploy {
			dc := &cc.Deploy[j]
			dv := DeployEditView{
				Type:         dc.Type,
				ReloadCmd:    dc.ReloadCmd,
				TestCmd:      dc.TestCmd,
				CertPath:     dc.CertPath,
				KeyPath:      dc.KeyPath,
				Host:         dc.Host,
				HostRef:      dc.HostRef,
				Port:         dc.Port,
				User:         dc.User,
				SSHKey:       dc.Key,
				RemotePath:   dc.RemotePath,
				CertFilename: dc.CertFilename,
				KeyFilename:  dc.KeyFilename,
				Dir:          dc.Dir,
				URL:          dc.URL,
			}
			cv.Deploys = append(cv.Deploys, dv)
		}
		view.Certificates = append(view.Certificates, cv)
	}
	return view, nil
}

// SaveConfig 保存前端表单到配置文件:密钥明文自动转为 {{env:VAR}} 引用并持久化到系统环境变量。
func (a *App) SaveConfig(view ConfigView) (string, error) {
	// 先处理密钥:明文 → 环境变量 + 引用(修改 view)
	envs, err := a.persistSecrets(&view)
	if err != nil {
		return "", err
	}
	cfg, err := a.buildConfig(view)
	if err != nil {
		return "", err
	}
	if err := cfg.Validate(); err != nil {
		return "", fmt.Errorf("配置校验失败: %w", err)
	}
	// 前端未传密钥(新增条目默认无密钥行)时,保留原配置中的密钥,防止误清空
	a.preserveSecrets(cfg)
	// 写回 YAML(原子)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("序列化配置: %w", err)
	}
	if err := atomicWriteFile(a.cfgPath(), data); err != nil {
		return "", fmt.Errorf("写入配置 %s: %w", a.cfgPath(), err)
	}
	// 开机自启设置(应用级偏好,独立于 yaml 配置;失败仅提示不阻断保存)
	if err := SetAutostart(view.Autostart); err != nil {
		return "", fmt.Errorf("配置已写入但开机自启设置失败: %w", err)
	}
	// 重新加载
	if err := a.reload(); err != nil {
		return "", fmt.Errorf("配置已写入但重载失败: %w", err)
	}
	msg := "配置已保存并生效"
	if len(envs) > 0 {
		msg += ";已写入环境变量:" + strings.Join(envs, ",")
	}
	a.logger.Printf("配置已保存到 %s%s", a.cfgPath(), msg)
	return msg, nil
}

// buildConfig 从视图构造 config.Config。
func (a *App) buildConfig(view ConfigView) (*config.Config, error) {
	cfg := &config.Config{
		CA: config.CAConfig{
			Server:     strings.TrimSpace(view.CAServer),
			Email:      strings.TrimSpace(view.CAEmail),
			AccountKey: strings.TrimSpace(view.AccountKey),
		},
		Notify: config.NotifyConfig{
			Webhook: strings.TrimSpace(view.Webhook),
			SMTP: config.SMTPConfig{
				Host: strings.TrimSpace(view.SMTPHost),
				Port: view.SMTPPort,
				User: strings.TrimSpace(view.SMTPUser),
				Pass: strings.TrimSpace(view.SMTPPass),
				To:   config.StringList(trimAll(view.SMTPTo)),
			},
			Events: config.EventConfig{
				Expiring: view.NotifyExpiring,
				Success:  view.NotifySuccess,
			},
		},
	}
	if cfg.CA.AccountKey == "" {
		cfg.CA.AccountKey = "./data/account.key"
	}
	for _, hv := range view.Hosts {
		cfg.Hosts = append(cfg.Hosts, config.HostConfig{
			Name:       strings.TrimSpace(hv.Name),
			Host:       strings.TrimSpace(hv.Host),
			Port:       hv.Port,
			User:       strings.TrimSpace(hv.User),
			Key:        strings.TrimSpace(hv.Key),
			RemotePath: strings.TrimSpace(hv.RemotePath),
			ReloadCmd:  strings.TrimSpace(hv.ReloadCmd),
			CertFilename: strings.TrimSpace(hv.CertFilename),
			KeyFilename:  strings.TrimSpace(hv.KeyFilename),
		})
	}
	// 调度(空值用默认)
	sched := config.DefaultSchedule()
	if v, err := time.ParseDuration(view.CheckInterval); err == nil {
		cfg.Schedule.CheckInterval = config.Duration(v)
	} else {
		cfg.Schedule.CheckInterval = sched.CheckInterval
	}
	if v, err := time.ParseDuration(view.RenewBefore); err == nil {
		cfg.Schedule.RenewBefore = config.Duration(v)
	} else {
		cfg.Schedule.RenewBefore = sched.RenewBefore
	}
	for _, s := range view.RetryBackoff {
		if v, err := time.ParseDuration(s); err == nil {
			cfg.Schedule.RetryBackoff = append(cfg.Schedule.RetryBackoff, config.Duration(v))
		}
	}
	if len(cfg.Schedule.RetryBackoff) == 0 {
		cfg.Schedule.RetryBackoff = sched.RetryBackoff
	}
	for _, cv := range view.Certificates {
		if strings.TrimSpace(cv.Name) == "" {
			return nil, fmt.Errorf("证书条目名称不能为空")
		}
		cc := config.CertificateConfig{
			Name:            strings.TrimSpace(cv.Name),
			Domains:         trimAll(cv.Domains),
			Challenge:       strings.TrimSpace(cv.Challenge),
			DNSProvider:     strings.TrimSpace(cv.DNSProvider),
			DNSProviderOpts: cv.DNSOpts,
			Storage:         config.StorageConfig{Dir: strings.TrimSpace(cv.StorageDir)},
		}
		for _, dv := range cv.Deploys {
			cc.Deploy = append(cc.Deploy, config.DeployConfig{
				Type:         dv.Type,
				ReloadCmd:    dv.ReloadCmd,
				TestCmd:      dv.TestCmd,
				CertPath:     dv.CertPath,
				KeyPath:      dv.KeyPath,
				Host:         dv.Host,
				HostRef:      dv.HostRef,
				Port:         dv.Port,
				User:         dv.User,
				Key:          dv.SSHKey,
				RemotePath:   dv.RemotePath,
				CertFilename: dv.CertFilename,
				KeyFilename:  dv.KeyFilename,
				Dir:          dv.Dir,
				URL:          dv.URL,
			})
		}
		cfg.Certificates = append(cfg.Certificates, cc)
	}
	return cfg, nil
}

// preserveSecrets 对每个条目:若前端未提供 DNS 密钥且原配置有,则保留原值。
func (a *App) preserveSecrets(cfg *config.Config) {
	if a.rawCfg == nil {
		return
	}
	for i := range cfg.Certificates {
		if len(cfg.Certificates[i].DNSProviderOpts) > 0 {
			continue
		}
		for j := range a.rawCfg.Certificates {
			if a.rawCfg.Certificates[j].Name == cfg.Certificates[i].Name && len(a.rawCfg.Certificates[j].DNSProviderOpts) > 0 {
				cfg.Certificates[i].DNSProviderOpts = a.rawCfg.Certificates[j].DNSProviderOpts
				a.logger.Printf("条目 %s: 前端未提供 DNS 密钥,保留原配置", cfg.Certificates[i].Name)
				break
			}
		}
	}
}

// persistSecrets 把 DNSOpts 与 SMTP 密码中的明文密钥转为 {{env:VAR}} 引用并写入系统环境变量。
func (a *App) persistSecrets(view *ConfigView) ([]string, error) {
	var envs []string
	// SMTP 授权码
	if !isEnvRef(view.SMTPPass) {
		val := strings.TrimSpace(view.SMTPPass)
		if val != "" {
			name := "GOZS_SMTP_PASS"
			if err := persistEnvFn(name, val); err != nil {
				return envs, fmt.Errorf("写入环境变量 %s: %w", name, err)
			}
			view.SMTPPass = "{{env:" + name + "}}"
			envs = append(envs, name)
		}
	} else if _, ok := os.LookupEnv(envRefName(view.SMTPPass)); !ok {
		return envs, fmt.Errorf("环境变量 %s 未设置,请在 SMTP 授权码栏填入明文", envRefName(view.SMTPPass))
	}
	for i := range view.Certificates {
		cv := &view.Certificates[i]
		if len(cv.DNSOpts) == 0 {
			continue
		}
		for k, v := range cv.DNSOpts {
			if isEnvRef(v) {
				// 已是引用:检查对应环境变量是否存在,缺失则提示补齐
				name := envRefName(v)
				if _, ok := os.LookupEnv(name); !ok {
					return envs, fmt.Errorf("环境变量 %s 未设置,请在密钥栏填入明文(将自动写入并保存为引用)", name)
				}
				continue
			}
			val := strings.TrimSpace(v)
			if val == "" {
				continue
			}
			// 非敏感参数(如轮询间隔/传播超时)直接明文落盘,不做 env 引用转换
			if dnsNonSecretOpt(k) {
				continue
			}
			name := envName(cv.Name, k)
			if err := persistEnvFn(name, val); err != nil {
				return envs, fmt.Errorf("写入环境变量 %s: %w", name, err)
			}
			cv.DNSOpts[k] = "{{env:" + name + "}}"
			envs = append(envs, name)
		}
	}
	return envs, nil
}

func envName(entry, key string) string {
	ascii := true
	for _, r := range entry {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			ascii = false
			break
		}
	}
	if ascii {
		return "GOZS_" + sanitizeEnv(entry) + "_" + sanitizeEnv(key)
	}
	// 中文等非 ASCII 条目名:用哈希保证唯一且为合法环境变量名(避免中文名互相覆盖)
	sum := sha1.Sum([]byte(entry))
	return fmt.Sprintf("GOZS_E%x_%s", sum[:4], sanitizeEnv(key))
}

func sanitizeEnv(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// persistEnv 把密钥持久化到系统环境变量。
// Windows:直接写注册表 HKCU\Environment(无弹窗,新进程自动读取);其他平台仅当前进程。
var persistEnvFn = persistEnv

func persistEnv(name, value string) error {
	if err := os.Setenv(name, value); err != nil {
		return err
	}
	return persistEnvPlatform(name, value)
}

func isEnvRef(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{{env:") && strings.HasSuffix(s, "}}")
}

func envRefName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{{env:")
	s = strings.TrimSuffix(s, "}}")
	return strings.TrimSpace(s)
}

func durString(d time.Duration) string {
	if d%(24*time.Hour) == 0 && d >= 24*time.Hour {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	if d%time.Hour == 0 && d >= time.Hour {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return d.String()
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// atomicWriteFile 原子写文件。
func atomicWriteFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
