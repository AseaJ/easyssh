// Package certmgr 定义证书托管的核心数据模型与生命周期状态机。
// 它是整个项目的底层依赖:acme/deploy/storage 均引用此包,不允许反向依赖。
package certmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Meta 是持久化到 meta.json 的条目状态,用于重启恢复与幂等判断。
type Meta struct {
	Name                string    `json:"name"`
	Domains             []string  `json:"domains"`
	NotBefore           time.Time `json:"not_before"`
	NotAfter            time.Time `json:"not_after"`
	Fingerprint         string    `json:"fingerprint"`          // 当前证书 leaf 的 sha256 指纹
	Issuer              string    `json:"issuer"`               // 签发 CA
	IssuedAt            time.Time `json:"issued_at"`            // 本地签发/续期时间
	FailCount           int       `json:"fail_count"`           // 连续失败次数(退避依据)
	LastError           string    `json:"last_error,omitempty"` // 最近一次错误
	NextRetryAt         time.Time `json:"next_retry_at"`        // 下次重试时间
	DeployedFingerprint string    `json:"deployed_fingerprint"` // 已部署到目标的指纹(幂等判断)
	DeployedTargets     []string  `json:"deployed_targets"`     // 已部署成功的目标名
	OrderURL            string    `json:"order_url,omitempty"`  // ACME 订单号
	LastDeployError     string    `json:"last_deploy_error,omitempty"`
	ExpiringNotifiedAt  time.Time `json:"expiring_notified_at,omitempty"` // 最近一次发送"即将到期"提醒的时间(避免重复推送)
}

// Bundle 是一份完整证书包:证书链 + 私钥 + 元数据。
// PEM 字段均为 PEM 编码字节;NotAfter 等取自 leaf 证书。
type Bundle struct {
	Name          string
	Domains       []string
	LeafPEM       []byte // 仅 leaf 证书
	FullchainPEM  []byte // leaf + intermediate(反向代理通用)
	ChainPEM      []byte // 仅 intermediate
	PrivateKeyPEM []byte // 私钥(PEM 编码)
	NotBefore     time.Time
	NotAfter      time.Time
	Fingerprint   string // leaf 的 sha256 指纹
	Issuer        string
	Meta          Meta
}

// FingerprintOf 计算 PEM 编码证书的 sha256 指纹(用于幂等与变更检测)。
func FingerprintOf(leafPEM []byte) string {
	sum := sha256.Sum256(leafPEM)
	return hex.EncodeToString(sum[:])
}

// Remaining 返回证书剩余有效期;已过期则返回负值。
func (b *Bundle) Remaining() time.Duration {
	return time.Until(b.NotAfter)
}

// ExpiringIn 判断剩余有效期是否 <= 阈值。
func (b *Bundle) ExpiringIn(threshold time.Duration) bool {
	return b.Remaining() <= threshold
}

// State 描述条目当前生命周期状态。
type State string

const (
	StateIdle   State = "idle"     // 无证书,待签发
	StateFresh  State = "fresh"    // 证书有效,无需动作
	StateRenew  State = "renewing" // 需要续期
	StateRetry  State = "retry"    // 失败,退避重试中
	StateFailed State = "failed"   // 连续失败超过上限
	StateDeploy State = "deploy"   // 部署中/待部署
)
