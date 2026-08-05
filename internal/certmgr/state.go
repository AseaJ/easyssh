package certmgr

import (
	"sort"
	"strconv"
	"time"
)

// Action 是调度器对单个条目的一次动作。
type Action string

const (
	ActionSkip    Action = "skip"    // 无需动作
	ActionIssue   Action = "issue"   // 无证书,签发
	ActionRenew   Action = "renew"   // 剩余有效期不足,续期
	ActionReissue Action = "reissue" // SAN 与配置不一致,重签
	ActionRetry   Action = "retry"   // 上次失败且已到重试时间
	ActionDeploy  Action = "deploy"  // 证书有效但尚未部署到全部目标
)

// Decision 是状态机的输出。
type Decision struct {
	Action Action
	Reason string
}

// Decide 根据配置域名、当前证书与阈值,决定条目动作。
// 优先级:无证书 > 失败重试(未到时间则跳过)> SAN 变化 > 到期续期 > 跳过。
func Decide(configuredDomains []string, b *Bundle, renewBefore time.Duration, now time.Time) Decision {
	if b == nil {
		return Decision{ActionIssue, "本地无证书"}
	}
	if b.Meta.FailCount > 0 {
		if now.Before(b.Meta.NextRetryAt) {
			return Decision{ActionSkip, "失败退避中,下次重试 " + b.Meta.NextRetryAt.Format(time.RFC3339)}
		}
		return Decision{ActionRetry, "上次失败,重试(第 " + strconv.Itoa(b.Meta.FailCount) + " 次)"}
	}
	if !sameDomains(configuredDomains, b.Domains) {
		return Decision{ActionReissue, "SAN 集合与配置不一致"}
	}
	if b.ExpiringIn(renewBefore) {
		return Decision{ActionRenew, "剩余有效期不足"}
	}
	return Decision{ActionSkip, "证书有效"}
}

// sameDomains 比较两个域名集合是否一致(顺序无关,不区分大小写)。
func sameDomains(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	na := normalize(a)
	nb := normalize(b)
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

func normalize(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

// NextRetryAfter 返回第 failCount 次失败后的下次重试间隔。
// failCount 从 1 开始;超过退避序列长度后封顶为最后一个间隔。
func NextRetryAfter(failCount int, backoffs []time.Duration) time.Duration {
	if len(backoffs) == 0 {
		return time.Hour
	}
	idx := failCount - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(backoffs) {
		idx = len(backoffs) - 1
	}
	return backoffs[idx]
}
