package bundle

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/asea/easyssh/internal/config"
	"gopkg.in/yaml.v3"
)

// 与 storage 布局一致的证书产物文件名。
const (
	fileCert      = "cert.pem"
	fileFullchain = "fullchain.pem"
	fileChain     = "chain.pem"
	filePrivkey   = "privkey.pem"
	fileMeta      = "meta.json"
)

// 包内 zip 条目路径常量(与 manifest.go 的导入方约定一致)。
const (
	entryManifest = "manifest.json"
	entryConfig   = "config.yaml"
	entrySecrets  = "secrets.enc"
	entrySSHKeys  = "ssh-keys.enc"
	certsDir      = "certs/"
)

// 环境变量引用正则:与 internal/config/env.go 的 envRefRe 保持一致。
var envRefRe = regexp.MustCompile(`\{\{env:([A-Za-z_][A-Za-z0-9_]*)\}\}`)

// BundleKind 标识包类型。
const BundleKind = "easyssh-bundle"

// zip 读取防护上限(防 zip bomb):包总大小 256MB、单条 64MB、条目数 4096。
const (
	maxBundleSize = 256 << 20
	maxEntrySize  = 64 << 20
	maxZipEntries = 4096
)

// SecretsPayload 是 secrets.enc 解密后的明文(env 密钥值 + ACME 账号密钥)。
type SecretsPayload struct {
	Env        map[string]string `json:"env"`
	AccountKey string            `json:"account_key,omitempty"`
}

// SSHKeysPayload 是 ssh-keys.enc 解密后的明文(原私钥路径 → PEM 内容)。
type SSHKeysPayload struct {
	Keys map[string]string `json:"keys"` // 路径 → 私钥内容
}

// ExportOptions 描述一次导出的范围与口令。
type ExportOptions struct {
	ConfigPath string // 配置文件路径(用于解析相对路径与读取配置)
	Scope      Scope
	Password   string // scope.NeedsPassword() 时必填
}

// Export 按 scope 收集配置/密钥/证书产物/SSH 私钥,打包为 .zsbundle 写入 outPath。
// 返回写入的 manifest(供调用方展示摘要)。
func Export(outPath string, opts ExportOptions) (*Manifest, error) {
	if opts.ConfigPath == "" {
		return nil, fmt.Errorf("配置文件路径不能为空")
	}
	if opts.Scope.NeedsPassword() {
		if err := validatePassword(opts.Password); err != nil {
			return nil, err
		}
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置: %w", err)
	}
	baseDir := filepath.Dir(opts.ConfigPath)

	m := &Manifest{
		FormatVersion: FormatVersion,
		Kind:          BundleKind,
		ExportedAt:    time.Now().Format(time.RFC3339),
		Scope:         opts.Scope,
		Warning:       warningText(opts.Scope),
	}

	// 1. 配置文本(不展开 env,保持 {{env:VAR}} 引用)
	cfgYAML, err := yamlMarshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("序列化配置: %w", err)
	}

	// 2. 收集环境变量引用名(从配置文本提取,保证与 config.yaml 一致)
	envNames := collectEnvRefs(cfgYAML)
	sort.Strings(envNames)

	// 3. 收集各范围数据
	var secrets *SecretsPayload
	var sshKeys *SSHKeysPayload
	var certFiles []zipFile
	if opts.Scope.Secrets {
		secrets, err = collectSecrets(cfg, envNames, baseDir)
		if err != nil {
			return nil, err
		}
		m.EnvSecrets = envNames
	}
	if opts.Scope.Certs {
		certFiles, err = collectCerts(cfg, baseDir)
		if err != nil {
			return nil, err
		}
		for _, c := range cfg.Certificates {
			m.Certs = append(m.Certs, CertRef{
				Name:       c.Name,
				Domains:    append([]string(nil), c.Domains...),
				HasPrivKey: true,
			})
		}
	}
	if opts.Scope.SSHKeys {
		sshKeys, err = collectSSHKeys(cfg, baseDir)
		if err != nil {
			return nil, err
		}
		for _, p := range sortedKeys(sshKeys.Keys) {
			m.SSHKeys = append(m.SSHKeys, SSHKeyRef{Path: p})
		}
	}

	// 4. 加密敏感段
	var secretsEnc, sshEnc []byte
	kdf := DefaultKDF()
	if opts.Scope.Secrets {
		plain, _ := json.Marshal(secrets)
		secretsEnc, err = encryptBlob(opts.Password, plain, kdf)
		if err != nil {
			return nil, fmt.Errorf("加密密钥段: %w", err)
		}
		m.KDF = kdf
	}
	if opts.Scope.SSHKeys {
		plain, _ := json.Marshal(sshKeys)
		sshEnc, err = encryptBlob(opts.Password, plain, kdf)
		if err != nil {
			return nil, fmt.Errorf("加密 SSH 密钥段: %w", err)
		}
		m.KDF = kdf
	}

	// 5. 写 zip(manifest 最后写入)
	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	entries := []zipFile{
		{entryConfig, cfgYAML},
	}
	if opts.Scope.Secrets {
		entries = append(entries, zipFile{entrySecrets, secretsEnc})
	}
	if opts.Scope.SSHKeys {
		entries = append(entries, zipFile{entrySSHKeys, sshEnc})
	}
	entries = append(entries, certFiles...)
	entries = append(entries, zipFile{entryManifest, manifestJSON})

	if err := writeZip(outPath, entries); err != nil {
		return nil, fmt.Errorf("写入导出包 %s: %w", outPath, err)
	}
	return m, nil
}

// --- 数据收集 ---

type zipFile struct {
	name string
	data []byte
}

func writeZip(path string, files []zipFile) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, zf := range files {
		w, err := zw.Create(zf.name)
		if err != nil {
			return err
		}
		if _, err := w.Write(zf.data); err != nil {
			return err
		}
	}
	return zw.Close()
}

// yamlMarshal 序列化配置(YAML);失败时返回错误。
func yamlMarshal(cfg *config.Config) ([]byte, error) {
	return yaml.Marshal(cfg)
}

func collectEnvRefs(yamlText []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range envRefRe.FindAllSubmatch(yamlText, -1) {
		name := string(m[1])
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// collectSecrets 收集环境变量明文值与 ACME 账号密钥。
// 环境变量缺失时报错(导出 secrets 档却缺值会让导入端拿不到密钥)。
func collectSecrets(cfg *config.Config, envNames []string, baseDir string) (*SecretsPayload, error) {
	sp := &SecretsPayload{Env: map[string]string{}}
	var missing []string
	for _, n := range envNames {
		v, ok := os.LookupEnv(n)
		if !ok {
			missing = append(missing, n)
			continue
		}
		sp.Env[n] = v
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("以下环境变量未设置,无法导出其密钥值: %s(请先补齐再导出)", strings.Join(missing, ", "))
	}
	if cfg.CA.AccountKey != "" {
		p := resolvePath(baseDir, cfg.CA.AccountKey)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("读取 ACME 账号密钥 %s: %w", p, err)
		}
		sp.AccountKey = string(data)
	}
	return sp, nil
}

// collectCerts 读取每个条目的证书产物目录(与 storage 布局一致)。
// storage.dir 为相对路径时基于配置目录解析。
func collectCerts(cfg *config.Config, baseDir string) ([]zipFile, error) {
	var files []zipFile
	for _, c := range cfg.Certificates {
		dir := resolvePath(baseDir, c.Storage.Dir)
		names := []string{fileCert, fileFullchain, fileChain, filePrivkey, fileMeta}
		for _, n := range names {
			p := filepath.Join(dir, n)
			data, err := os.ReadFile(p)
			if err != nil {
				if os.IsNotExist(err) {
					continue // 未签发的条目可能没有产物
				}
				return nil, fmt.Errorf("读取证书产物 %s: %w", p, err)
			}
			files = append(files, zipFile{certsDir + c.Name + "/" + n, data})
		}
	}
	return files, nil
}

// collectSSHKeys 收集 hosts 与 deploy.ssh 引用的私钥文件(按路径去重)。
func collectSSHKeys(cfg *config.Config, baseDir string) (*SSHKeysPayload, error) {
	p := &SSHKeysPayload{Keys: map[string]string{}}
	add := func(key string) error {
		if key == "" || p.Keys[key] != "" {
			return nil
		}
		full := resolvePath(baseDir, key)
		data, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("读取 SSH 私钥 %s: %w", full, err)
		}
		p.Keys[key] = string(data)
		return nil
	}
	for i := range cfg.Hosts {
		if err := add(cfg.Hosts[i].Key); err != nil {
			return nil, err
		}
	}
	for i := range cfg.Certificates {
		for j := range cfg.Certificates[i].Deploy {
			if cfg.Certificates[i].Deploy[j].Type == "ssh" {
				if err := add(cfg.Certificates[i].Deploy[j].Key); err != nil {
					return nil, err
				}
			}
		}
	}
	return p, nil
}

// resolvePath 将配置中的相对路径(相对配置文件目录)解析为绝对路径;~ 展开为主目录。
func resolvePath(baseDir, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	return filepath.Clean(p)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
