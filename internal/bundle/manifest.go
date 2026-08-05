package bundle

// FormatVersion 是当前包格式版本;导入时校验,不匹配拒绝。
const FormatVersion = 1

// Scope 描述导出包包含的内容范围。
// Config 恒为 true(config 是底座);Secrets/SSHKeys 需要口令加密。
type Scope struct {
	Config  bool `json:"config"`
	Secrets bool `json:"secrets"`
	Certs   bool `json:"certs"`
	SSHKeys bool `json:"ssh_keys"`
}

// NeedsPassword 报告该范围是否需要口令加密(含密钥或 SSH 私钥时必须)。
func (s Scope) NeedsPassword() bool { return s.Secrets || s.SSHKeys }

// KDF 记录口令派生密钥的算法与参数,解密时按此复算。
// 算法升级(如 argon2id)时加 algo 分支,格式版本仍兼容。
type KDF struct {
	Algo   string `json:"algo"` // 当前仅 "scrypt"
	N      int    `json:"n"`
	R      int    `json:"r"`
	P      int    `json:"p"`
	KeyLen int    `json:"key_len"`
}

// DefaultKDF 返回默认 scrypt 参数(N=2^17 ≈ 秒级,防暴力破解)。
func DefaultKDF() KDF {
	return KDF{Algo: "scrypt", N: 1 << 17, R: 8, P: 1, KeyLen: 32}
}

// CertRef 描述包内一个证书条目的产物。
type CertRef struct {
	Name       string   `json:"name"`
	Domains    []string `json:"domains"`
	HasPrivKey bool     `json:"has_privkey"`
}

// SSHKeyRef 描述包内一个 SSH 私钥(去重后的 key 路径)。
type SSHKeyRef struct {
	Name string `json:"name"` // 引用方:host:<name> 或 cert:<条目名>
	Path string `json:"path"` // 原始路径(相对或绝对)
	Algo string `json:"algo,omitempty"`
}

// Manifest 是导出包根部的明文清单。内容先于 manifest 写入 zip,保证清单完整可信。
type Manifest struct {
	FormatVersion int         `json:"format_version"`
	Kind          string      `json:"kind"` // "go-zs-bundle"
	ExportedAt    string      `json:"exported_at"`
	AppVersion    string      `json:"app_version,omitempty"`
	Scope         Scope       `json:"scope"`
	KDF           KDF         `json:"kdf,omitempty"`
	Certs         []CertRef   `json:"certs,omitempty"`
	SSHKeys       []SSHKeyRef `json:"ssh_keys,omitempty"`
	EnvSecrets    []string    `json:"env_secrets,omitempty"`
	Warning       string      `json:"warning,omitempty"`
}

// validate 校验清单格式与必需字段。
func (m *Manifest) validate() error {
	if m == nil {
		return ErrFormat("清单缺失")
	}
	if m.FormatVersion != FormatVersion {
		return ErrFormat("不支持的格式版本 %d(当前支持 %d)", m.FormatVersion, FormatVersion)
	}
	if m.Kind != BundleKind {
		return ErrFormat("不是 go-zs 导出包(kind=%q)", m.Kind)
	}
	if m.Scope.Secrets || m.Scope.SSHKeys {
		if m.KDF.Algo != "scrypt" || m.KDF.N <= 0 || m.KDF.R <= 0 || m.KDF.P <= 0 || m.KDF.KeyLen <= 0 {
			return ErrFormat("清单中 KDF 参数缺失或非法")
		}
	}
	return nil
}

// warningText 根据范围生成包内警告文案。
func warningText(s Scope) string {
	if s.Secrets || s.SSHKeys || s.Certs {
		return "本包包含证书私钥/密钥/SSH 私钥等敏感数据,请经安全渠道(加密传输/当面拷贝)传递;口令即唯一防线,口令丢失无法恢复。"
	}
	return "本包仅含配置(无密钥),可安全分发;导入后需在目标机补齐 {{env:VAR}} 引用的密钥。"
}
