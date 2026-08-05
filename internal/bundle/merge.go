package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asea/easyssh/internal/config"
	"gopkg.in/yaml.v3"
)

// ConflictMode 定义导入时与目标机现有配置冲突的处理方式。
type ConflictMode string

const (
	ConflictAppend    ConflictMode = "append"    // 冲突项改名追加(host_ref 联动改名),其余项导入
	ConflictSkip      ConflictMode = "skip"      // 跳过冲突项(目标机已有同名条目)
	ConflictOverwrite ConflictMode = "overwrite" // 用包内内容覆盖目标机同名词条(仅条目级;hosts 永不覆盖)
)

// ParseConflict 解析冲突策略字符串,非法时返回错误。
func ParseConflict(s string) (ConflictMode, error) {
	switch ConflictMode(strings.TrimSpace(strings.ToLower(s))) {
	case ConflictAppend:
		return ConflictAppend, nil
	case ConflictSkip:
		return ConflictSkip, nil
	case ConflictOverwrite:
		return ConflictOverwrite, nil
	default:
		return "", fmt.Errorf("非法冲突策略 %q(可选: append/skip/overwrite)", s)
	}
}

// ImportResult 描述一次导入的结果,供调用方展示。
type ImportResult struct {
	ImportedCerts []string // 实际导入(或覆盖)的证书条目名
	ImportedHosts []string // 实际导入的 hosts 条目名
	Renamed       []string // 因冲突被改名的条目(格式 "原 -> 新")
	Skipped       []string // 被跳过的冲突项(skip 模式)
	EnvSecrets    []string // 已写入的 env 密钥变量名
	SSHKeys       []string // 已落盘的 SSH 私钥路径
	MissingEnv    []string // 配置引用但未提供的 env 变量(导入后需补齐)
	Notes         []string // 提示信息(如 SSH 目标需人工配密钥)
}

// MergeOptions 是导入合并的参数。
type MergeOptions struct {
	ConfigPath string       // 目标配置文件路径(合并后写回)
	Password   string       // 解密口令(含加密段时必填;ReadBundle 已用,此处保留备用)
	Conflict   ConflictMode // 冲突策略
	TargetBase string       // 目标机路径基准:相对路径基于它解析(默认配置文件所在目录)
	// PersistEnv 把 env 密钥写入系统环境变量(复用 app.persistEnv 的注册表持久化)。
	PersistEnv func(name, value string) error
}

// ApplyImport 将 bundle 合并进目标配置并落盘。
// 流程:解析包配置 → 合并 hosts/证书(冲突处理 + host_ref 联动改名)
// → 写 env 密钥 / account.key / 证书产物 / SSH 私钥 → 原子写回配置 → 返回结果。
func ApplyImport(b *Bundle, opts MergeOptions) (*ImportResult, error) {
	if opts.ConfigPath == "" {
		return nil, fmt.Errorf("目标配置文件路径不能为空")
	}
	if b.Manifest == nil {
		return nil, ErrFormat("包内缺少清单")
	}
	bcfg, err := parseBundleConfig(b.Config)
	if err != nil {
		return nil, err
	}
	if opts.TargetBase == "" {
		opts.TargetBase = filepath.Dir(opts.ConfigPath)
	}

	// 目标配置(可能不存在:全新导入)
	tcfg := &config.Config{}
	if _, err := os.Stat(opts.ConfigPath); err == nil {
		tcfg, err = config.Load(opts.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("读取目标配置: %w", err)
		}
	} else {
		// 全新导入:以包内配置的全局段(CA/Schedule/Notify)为基底,
		// hosts/certificates 走合并逻辑(此时无冲突,全部导入)。
		tcfg.CA = bcfg.CA
		tcfg.Schedule = bcfg.Schedule
		tcfg.Notify = bcfg.Notify
	}

	res := &ImportResult{}
	// 1. hosts 合并,得改名映射
	renameMap := map[string]string{}
	res.ImportedHosts, res.Renamed, res.Skipped, renameMap, err = mergeHosts(tcfg, bcfg, opts.Conflict)
	if err != nil {
		return nil, err
	}
	// 2. 证书条目合并(host_ref 联动改名)
	res.ImportedCerts, err = mergeCerts(tcfg, bcfg, opts.Conflict, renameMap)
	if err != nil {
		return nil, err
	}
	// 3. env 密钥 + account.key
	if b.Secrets != nil {
		if opts.PersistEnv == nil {
			return nil, fmt.Errorf("需要 PersistEnv 回调以写入密钥")
		}
		missing, err := applySecrets(b.Secrets, tcfg, opts.TargetBase, opts.PersistEnv)
		if err != nil {
			return nil, err
		}
		res.EnvSecrets = sortedKeys(b.Secrets.Env)
		res.MissingEnv = append(res.MissingEnv, missing...)
	}
	// 4. 证书产物
	if len(b.Certs) > 0 {
		if err := applyCerts(b.Certs, tcfg, opts.TargetBase); err != nil {
			return nil, err
		}
	}
	// 5. SSH 私钥落盘
	if b.SSHKeys != nil {
		written, err := applySSHKeys(b.SSHKeys, opts.TargetBase)
		if err != nil {
			return nil, err
		}
		res.SSHKeys = written
	}
	// 6. 原子写回配置
	if err := atomicWriteConfig(opts.ConfigPath, tcfg); err != nil {
		return nil, err
	}
	// 7. 导入后缺失的 env 引用(用于提示)
	cfgYAML, _ := yaml.Marshal(tcfg)
	for _, n := range collectEnvRefs(cfgYAML) {
		if _, ok := os.LookupEnv(n); !ok {
			res.MissingEnv = append(res.MissingEnv, n)
		}
	}
	// 8. 提示:未携带 SSH 私钥的部署目标需人工配密钥
	if b.SSHKeys == nil || len(b.SSHKeys.Keys) == 0 {
		for i := range tcfg.Certificates {
			for j := range tcfg.Certificates[i].Deploy {
				d := &tcfg.Certificates[i].Deploy[j]
				if d.Type == "ssh" && d.Key != "" {
					res.Notes = append(res.Notes,
						fmt.Sprintf("条目 %s 的 SSH 部署未包含私钥,导入后需在目标机配置密钥(路径 %s)",
							tcfg.Certificates[i].Name, d.Key))
				}
			}
		}
	}
	return res, nil
}

// parseBundleConfig 解析包内 config.yaml(含默认值与校验)。
func parseBundleConfig(data []byte) (*config.Config, error) {
	cfg := &config.Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, ErrFormat("解析包内配置失败: %v", err)
	}
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("包内配置校验失败: %w", err)
	}
	return cfg, nil
}

// mergeHosts 合并 hosts;返回导入的 host 名、改名记录(原 -> 新)、跳过列表、改名映射。
func mergeHosts(t, b *config.Config, mode ConflictMode) (imported, renamed, skipped []string, renameMap map[string]string, err error) {
	existing := map[string]bool{}
	for i := range t.Hosts {
		existing[t.Hosts[i].Name] = true
	}
	renameMap = map[string]string{}
	for i := range b.Hosts {
		name := b.Hosts[i].Name
		switch {
		case !existing[name]:
			t.Hosts = append(t.Hosts, b.Hosts[i])
			imported = append(imported, name)
			existing[name] = true
		case mode == ConflictAppend:
			newName := uniqueName(name, existing)
			nh := b.Hosts[i]
			nh.Name = newName
			t.Hosts = append(t.Hosts, nh)
			renamed = append(renamed, name+" -> "+newName)
			renameMap[name] = newName
			existing[newName] = true
		default: // ConflictSkip / ConflictOverwrite(hosts 永不覆盖,一律跳过)
			skipped = append(skipped, name)
		}
	}
	return imported, renamed, skipped, renameMap, nil
}

// mergeCerts 合并证书条目;hostRefRenames 为 hosts 改名映射(原 → 新),同步更新 deploy.host_ref。
func mergeCerts(t, b *config.Config, mode ConflictMode, hostRefRenames map[string]string) (imported []string, err error) {
	existing := map[string]bool{}
	for i := range t.Certificates {
		existing[t.Certificates[i].Name] = true
	}
	for i := range b.Certificates {
		cc := b.Certificates[i]
		applyHostRefRenames(&cc, hostRefRenames)
		name := cc.Name
		switch {
		case !existing[name]:
			t.Certificates = append(t.Certificates, cc)
			imported = append(imported, name)
			existing[name] = true
		case mode == ConflictAppend:
			newName := uniqueName(name, existing)
			cc.Name = newName
			t.Certificates = append(t.Certificates, cc)
			imported = append(imported, newName)
			existing[newName] = true
		case mode == ConflictOverwrite:
			for j := range t.Certificates {
				if t.Certificates[j].Name == name {
					t.Certificates[j] = cc
					imported = append(imported, name)
					break
				}
			}
		default: // ConflictSkip
		}
	}
	return imported, nil
}

// applyHostRefRenames 把证书条目 deploy 中的 host_ref 按映射改名。
func applyHostRefRenames(cc *config.CertificateConfig, renames map[string]string) {
	if len(renames) == 0 {
		return
	}
	for j := range cc.Deploy {
		if ref := cc.Deploy[j].HostRef; ref != "" {
			if nn, ok := renames[ref]; ok {
				cc.Deploy[j].HostRef = nn
			}
		}
	}
}

// uniqueName 在已有集合中生成唯一名(base, base-2, base-3 …)。
func uniqueName(base string, existing map[string]bool) string {
	if !existing[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !existing[cand] {
			return cand
		}
	}
}

// applySecrets 写 env 密钥到系统环境变量,并把 account.key 落盘到目标配置指定路径。
// 返回缺失的 env 变量名(值为空的)。
func applySecrets(sp *SecretsPayload, t *config.Config, targetBase string, persist func(name, value string) error) (missing []string, err error) {
	for n, v := range sp.Env {
		if v == "" {
			missing = append(missing, n)
			continue
		}
		if err := persist(n, v); err != nil {
			return missing, fmt.Errorf("写入环境变量 %s: %w", n, err)
		}
	}
	if sp.AccountKey != "" && t.CA.AccountKey != "" {
		p := resolvePath(targetBase, t.CA.AccountKey)
		if err := osWriteFile(p, []byte(sp.AccountKey)); err != nil {
			return missing, fmt.Errorf("写入 ACME 账号密钥 %s: %w", p, err)
		}
	}
	return missing, nil
}

// applyCerts 将包内证书产物写入目标配置各条目 storage.dir(条目未导入时跳过)。
// storage.dir 为相对路径时基于 targetBase 解析。
func applyCerts(certs map[string]map[string][]byte, t *config.Config, targetBase string) error {
	dirByName := map[string]string{}
	for i := range t.Certificates {
		dirByName[t.Certificates[i].Name] = resolvePath(targetBase, t.Certificates[i].Storage.Dir)
	}
	for name, files := range certs {
		dir, ok := dirByName[name]
		if !ok {
			continue
		}
		for fn, data := range files {
			p := filepath.Join(dir, fn)
			if err := osWriteFile(p, data); err != nil {
				return fmt.Errorf("写入证书产物 %s: %w", p, err)
			}
		}
	}
	return nil
}

// applySSHKeys 将 SSH 私钥内容落盘到目标机,返回落盘路径。
func applySSHKeys(sp *SSHKeysPayload, targetBase string) ([]string, error) {
	var written []string
	for origPath, content := range sp.Keys {
		p := resolvePath(targetBase, origPath)
		if err := osWriteFile(p, []byte(content)); err != nil {
			return written, fmt.Errorf("写入 SSH 私钥 %s: %w", p, err)
		}
		written = append(written, p)
	}
	return written, nil
}

func atomicWriteConfig(path string, cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	return osWriteFile(path, data)
}

func osWriteFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o600)
}
