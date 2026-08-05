package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 支持 "6h"、"30d" 形式的 YAML 时长解析。
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration 必须是字符串(如 6h/30d): %w", err)
	}
	v, err := parseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML 序列化回字符串形式("6h"、"30d"),保证界面保存后配置可读。
func (d Duration) MarshalYAML() (interface{}, error) {
	td := time.Duration(d)
	if td%24*time.Hour == 0 && td >= 24*time.Hour {
		return fmt.Sprintf("%dd", td/(24*time.Hour)), nil
	}
	if td%time.Hour == 0 && td >= time.Hour {
		return fmt.Sprintf("%dh", td/time.Hour), nil
	}
	return td.String(), nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// parseDuration 支持 "30d" 天单位(Go 原生无)。
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := parseFloat(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("无效时长 %q: %w", s, err)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// Config 是 go-zs 的完整配置。
type Config struct {
	CA           CAConfig            `yaml:"ca"`
	Hosts        []HostConfig        `yaml:"hosts,omitempty"` // SSH 目标定义(可被 deploy 按 host_ref 引用)
	Certificates []CertificateConfig `yaml:"certificates"`
	Schedule     ScheduleConfig      `yaml:"schedule"`
	Notify       NotifyConfig        `yaml:"notify,omitempty"`
}

// HostConfig 是可复用的 SSH 目标定义。
type HostConfig struct {
	Name         string `yaml:"name"` // 引用名(deploy.host_ref)
	Host         string `yaml:"host"`
	Port         int    `yaml:"port,omitempty"`
	User         string `yaml:"user"`
	Key          string `yaml:"key,omitempty"`
	KnownHosts   string `yaml:"known_hosts,omitempty"` // known_hosts 路径(推荐;为空则拒绝连接)
	RemotePath   string `yaml:"remote_path,omitempty"`
	ReloadCmd    string `yaml:"reload_cmd,omitempty"`
	CertFilename string `yaml:"cert_filename,omitempty"` // 可选:远程证书文件名(默认 fullchain.pem)
	KeyFilename  string `yaml:"key_filename,omitempty"`  // 可选:远程私钥文件名(默认 privkey.pem)
}

// NotifyConfig 是通知配置。Events 控制哪些事件触发推送(默认全部关闭,需显式开启)。
type NotifyConfig struct {
	Webhook string      `yaml:"webhook,omitempty"` // 推送 URL(可选)
	SMTP    SMTPConfig  `yaml:"smtp,omitempty"`    // 邮件推送(可选)
	Events  EventConfig `yaml:"events,omitempty"`  // 事件开关
}

// EventConfig 是通知事件开关。
type EventConfig struct {
	Expiring bool `yaml:"expiring,omitempty"` // 即将到期提醒(进入续期窗口时)
	Success  bool `yaml:"success,omitempty"`  // 签发/续期/部署成功提醒(含新到期时间)
}

// SMTPConfig 是邮件推送配置。
type SMTPConfig struct {
	Host string     `yaml:"host,omitempty"`
	Port int        `yaml:"port,omitempty"`
	User string     `yaml:"user,omitempty"`
	Pass string     `yaml:"pass,omitempty"` // 授权码,建议 {{env:VAR}}
	To   StringList `yaml:"to,omitempty"`   // 收件人,支持单个字符串或列表
}

// StringList 兼容"单个字符串"与"字符串列表"两种 YAML 写法(用于收件人等字段)。
type StringList []string

// UnmarshalYAML 实现 yaml.v3 的字符串/列表双兼容解析。
func (l *StringList) UnmarshalYAML(node *yaml.Node) error {
	var single string
	if err := node.Decode(&single); err == nil {
		if single != "" {
			*l = []string{single}
		}
		return nil
	}
	var list []string
	if err := node.Decode(&list); err != nil {
		return fmt.Errorf("字段需要字符串或字符串列表: %w", err)
	}
	*l = list
	return nil
}

// MarshalYAML 序列化为字符串列表。
func (l StringList) MarshalYAML() (interface{}, error) {
	if len(l) == 1 {
		return l[0], nil // 单收件人保持简洁
	}
	return []string(l), nil
}

type CAConfig struct {
	Server     string `yaml:"server"`      // ACME directory URL
	Email      string `yaml:"email"`       // 账号邮箱
	AccountKey string `yaml:"account_key"` // 账号密钥路径
}

type CertificateConfig struct {
	Name            string            `yaml:"name"`
	Domains         []string          `yaml:"domains"`
	Challenge       string            `yaml:"challenge"` // http-01 | dns-01
	DNSProvider     string            `yaml:"dns_provider,omitempty"`
	DNSProviderOpts map[string]string `yaml:"dns_provider_opts,omitempty"`
	Storage         StorageConfig     `yaml:"storage"`
	Deploy          []DeployConfig    `yaml:"deploy,omitempty"`
}

type StorageConfig struct {
	Dir string `yaml:"dir"`
}

// DeployConfig 是部署目标配置,按 Type 区分各类型参数。
type DeployConfig struct {
	Type string `yaml:"type"` // nginx | ssh | file | webhook

	// nginx
	ReloadCmd string `yaml:"reload_cmd,omitempty"`
	TestCmd   string `yaml:"test_cmd,omitempty"`  // 配置校验命令,默认 nginx -t
	CertPath  string `yaml:"cert_path,omitempty"` // 自定义证书落盘路径(默认 <dir>/fullchain.pem)
	KeyPath   string `yaml:"key_path,omitempty"`  // 自定义私钥落盘路径(默认 <dir>/privkey.pem)

	// ssh
	Host         string `yaml:"host,omitempty"`
	HostRef      string `yaml:"host_ref,omitempty"` // 引用 hosts 定义(推荐)
	Port         int    `yaml:"port,omitempty"`
	User         string `yaml:"user,omitempty"`
	Key          string `yaml:"key,omitempty"`
	KnownHosts   string `yaml:"known_hosts,omitempty"` // known_hosts 路径(推荐;为空则拒绝连接)
	RemotePath   string `yaml:"remote_path,omitempty"`
	CertFilename string `yaml:"cert_filename,omitempty"` // 可选:覆盖 host 的远程证书文件名
	KeyFilename  string `yaml:"key_filename,omitempty"`  // 可选:覆盖 host 的远程私钥文件名

	// file
	Dir string `yaml:"dir,omitempty"`

	// webhook
	URL string `yaml:"url,omitempty"`
}

type ScheduleConfig struct {
	CheckInterval Duration   `yaml:"check_interval"`
	RenewBefore   Duration   `yaml:"renew_before"`
	RetryBackoff  []Duration `yaml:"retry_backoff"`
}

// Defaults 返回带默认值的调度配置(条目级无默认)。
func DefaultSchedule() ScheduleConfig {
	return ScheduleConfig{
		CheckInterval: Duration(6 * time.Hour),
		RenewBefore:   Duration(30 * 24 * time.Hour),
		RetryBackoff: []Duration{
			Duration(time.Hour), Duration(6 * time.Hour),
			Duration(24 * time.Hour), Duration(72 * time.Hour),
		},
	}
}

// Load 从路径读取并解析配置,含默认值填充与校验。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置校验失败: %w", err)
	}
	return &cfg, nil
}

// LoadAndExpand 读取配置并展开 {{env:VAR}} 引用;缺失环境变量视为错误。
func LoadAndExpand(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if err := ExpandEnvRefs(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadAndExpandLenient 与 LoadAndExpand 相同,但缺失的环境变量保留引用
// 并在返回的 missing 中列出(不视为致命错误),供 GUI 提示用户补齐密钥。
func LoadAndExpandLenient(path string) (*Config, []string, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, nil, err
	}
	missing, err := expandEnvRefsLenient(cfg)
	if err != nil {
		return nil, nil, err
	}
	return cfg, missing, nil
}

func (c *Config) SetDefaults() {
	if c.CA.Server == "" {
		c.CA.Server = "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
	if c.CA.AccountKey == "" {
		c.CA.AccountKey = "./data/account.key"
	}
	sched := DefaultSchedule()
	if c.Schedule.CheckInterval == 0 {
		c.Schedule.CheckInterval = sched.CheckInterval
	}
	if c.Schedule.RenewBefore == 0 {
		c.Schedule.RenewBefore = sched.RenewBefore
	}
	if len(c.Schedule.RetryBackoff) == 0 {
		c.Schedule.RetryBackoff = sched.RetryBackoff
	}
}

var (
	labelRe    = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
	wildcardRe = regexp.MustCompile(`^\*\.`)
)

// Validate 校验配置合法性,返回第一个错误。
func (c *Config) Validate() error {
	if c.CA.Server == "" {
		return errors.New("ca.server 不能为空")
	}
	if u, err := url.Parse(c.CA.Server); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("ca.server 必须是 http(s) URL: %s", c.CA.Server)
	}
	if len(c.Certificates) == 0 {
		return errors.New("至少需要一个 certificates 条目")
	}
	// 校验 hosts 定义(名称唯一、字段合法)
	hostNames := map[string]bool{}
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if h.Name == "" {
			return errors.New("hosts 条目 name 不能为空")
		}
		if hostNames[h.Name] {
			return fmt.Errorf("hosts 条目名重复: %s", h.Name)
		}
		hostNames[h.Name] = true
		if h.Host == "" || h.User == "" {
			return fmt.Errorf("hosts 条目 %s 需要 host 与 user", h.Name)
		}
		if h.Port == 0 {
			h.Port = 22
		}
		// 证书/私钥文件名可选;提供时必须为安全的纯文件名(禁止路径分隔与 .. 穿越)
		for _, fn := range []struct {
			name, v string
		}{{"cert_filename", h.CertFilename}, {"key_filename", h.KeyFilename}} {
			if fn.v == "" {
				continue
			}
			if !validRemoteFilename(fn.v) {
				return fmt.Errorf("hosts 条目 %s 的 %s 非法: %q(只允许文件名,不能含路径分隔符或 ..)", h.Name, fn.name, fn.v)
			}
		}
	}
	seenNames := map[string]bool{}
	seenDirs := map[string]bool{}
	for i := range c.Certificates {
		cc := &c.Certificates[i]
		if err := cc.validate(); err != nil {
			return fmt.Errorf("证书条目 %d(%s): %w", i, cc.Name, err)
		}
		if seenNames[cc.Name] {
			return fmt.Errorf("证书条目名重复: %s", cc.Name)
		}
		seenNames[cc.Name] = true
		if seenDirs[cc.Storage.Dir] {
			return fmt.Errorf("多个条目使用同一存储目录: %s", cc.Storage.Dir)
		}
		seenDirs[cc.Storage.Dir] = true
		// 校验 deploy 的 host_ref 引用存在
		for j := range cc.Deploy {
			ref := cc.Deploy[j].HostRef
			if ref != "" && !hostNames[ref] {
				return fmt.Errorf("证书条目 %s 的 deploy[%d] 引用了不存在的 host: %s", cc.Name, j, ref)
			}
		}
	}
	return nil
}

func (cc *CertificateConfig) validate() error {
	if cc.Name == "" {
		return errors.New("name 不能为空")
	}
	if len(cc.Domains) == 0 {
		return errors.New("domains 至少需要一个域名")
	}
	hasWildcard := false
	for _, d := range cc.Domains {
		if err := validateDomain(d); err != nil {
			return fmt.Errorf("域名 %q 非法: %w", d, err)
		}
		if wildcardRe.MatchString(d) {
			hasWildcard = true
		}
	}
	switch cc.Challenge {
	case "http-01", "dns-01":
	default:
		return fmt.Errorf("challenge 必须是 http-01 或 dns-01,实际为 %q", cc.Challenge)
	}
	if hasWildcard && cc.Challenge != "dns-01" {
		return errors.New("包含通配符域名时 challenge 必须为 dns-01(http-01 不支持通配符)")
	}
	if cc.Challenge == "dns-01" && cc.DNSProvider == "" {
		return errors.New("challenge 为 dns-01 时必须配置 dns_provider")
	}
	if cc.Storage.Dir == "" {
		return errors.New("storage.dir 不能为空")
	}
	for i := range cc.Deploy {
		if err := cc.Deploy[i].validate(); err != nil {
			return fmt.Errorf("deploy[%d]: %w", i, err)
		}
	}
	return nil
}

// validRemoteFilename 校验远程文件名安全:仅允许字母/数字/_/-/. ,且不含路径分隔与 .. 穿越。
func validRemoteFilename(name string) bool {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

func validateDomain(d string) error {
	d = strings.TrimSuffix(d, ".")
	if d == "" {
		return errors.New("空域名")
	}
	if len(d) > 253 {
		return errors.New("长度超过 253")
	}
	d = strings.TrimPrefix(d, "*.")
	if strings.Contains(d, "*") {
		return errors.New("通配符只能作为最左侧标签")
	}
	// 简单 hostname/IP 校验:标签格式
	if ip := net.ParseIP(d); ip != nil {
		return nil
	}
	for _, label := range strings.Split(d, ".") {
		if !labelRe.MatchString(label) {
			return fmt.Errorf("标签 %q 非法", label)
		}
	}
	return nil
}

func (dc *DeployConfig) validate() error {	switch dc.Type {
	case "nginx":
		if dc.ReloadCmd == "" {
			dc.ReloadCmd = "nginx -s reload" // 填默认
		}
	case "ssh":
		if dc.HostRef == "" && (dc.Host == "" || dc.User == "") {
			return errors.New("ssh 部署需要 host_ref(引用 hosts 定义)或 host/user")
		}
		if dc.Port == 0 {
			dc.Port = 22
		}
	case "file":
		if dc.Dir == "" {
			return errors.New("file 部署需要 dir")
		}
	case "webhook":
		if dc.URL == "" {
			return errors.New("webhook 部署需要 url")
		}
		if u, err := url.Parse(dc.URL); err != nil || u.Scheme == "" {
			return fmt.Errorf("webhook url 非法: %s", dc.URL)
		}
	default:
		return fmt.Errorf("未知 deploy 类型 %q(可选: nginx/ssh/file/webhook)", dc.Type)
	}
	return nil
}

// Clone 深拷贝配置。reload 时基于副本解析路径,避免污染展示/保存用的原始配置。
func Clone(c *Config) *Config {
	if c == nil {
		return nil
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return c
	}
	var out Config
	if err := yaml.Unmarshal(data, &out); err != nil {
		return c
	}
	return &out
}

// ResolvePaths 将配置中所有「本地文件路径」字段基于 baseDir(配置文件所在目录)
// 解析为绝对路径,并展开 ~ 为主目录。原地修改传入的 cfg。
// 仅处理本地路径:ca.account_key、hosts[].key、certificates[].storage.dir,
// 以及 deploy 的本地字段(ssh 私钥 key、nginx 落盘 cert_path/key_path、file 的 dir)。
// 远程路径(remote_path、cert_filename、key_filename)与 URL 不处理。
func ResolvePaths(cfg *Config, baseDir string) {
	abs := func(p string) string {
		p = strings.TrimSpace(p)
		if p == "" {
			return ""
		}
		if p == "~" {
			if home, err := os.UserHomeDir(); err == nil {
				return home
			}
			return p
		}
		if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(baseDir, p)
		}
		return filepath.Clean(p)
	}
	cfg.CA.AccountKey = abs(cfg.CA.AccountKey)
	for i := range cfg.Hosts {
		cfg.Hosts[i].Key = abs(cfg.Hosts[i].Key)
	}
	for i := range cfg.Certificates {
		cc := &cfg.Certificates[i]
		cc.Storage.Dir = abs(cc.Storage.Dir)
		for j := range cc.Deploy {
			d := &cc.Deploy[j]
			d.Key = abs(d.Key)
			d.CertPath = abs(d.CertPath)
			d.KeyPath = abs(d.KeyPath)
			d.Dir = abs(d.Dir)
		}
	}
}
