package certmgr

import (
	"testing"
	"time"

	"github.com/asea/easyssh/internal/testutil"
)

const renewBefore = 30 * 24 * time.Hour

// bundle 构造带证书的测试 Bundle。
func bundle(domains []string, notAfter time.Time) *Bundle {
	leaf, key := testutil.GenSelfSigned(domains, notAfter)
	return &Bundle{
		Name:          "a",
		Domains:       domains,
		LeafPEM:       leaf,
		FullchainPEM:  leaf,
		PrivateKeyPEM: key,
		NotBefore:     time.Now().Add(-time.Hour),
		NotAfter:      notAfter,
		Fingerprint:   FingerprintOf(leaf),
	}
}

func TestDecideNoCert(t *testing.T) {
	d := Decide([]string{"a.com"}, nil, renewBefore, time.Now())
	if d.Action != ActionIssue {
		t.Fatalf("无证书应 issue,实际 %s", d.Action)
	}
}

func TestDecideFresh(t *testing.T) {
	b := bundle([]string{"a.com"}, time.Now().Add(60*24*time.Hour))
	d := Decide([]string{"a.com"}, b, renewBefore, time.Now())
	if d.Action != ActionSkip {
		t.Fatalf("证书有效应 skip,实际 %s(%s)", d.Action, d.Reason)
	}
}

func TestDecideRenew(t *testing.T) {
	b := bundle([]string{"a.com"}, time.Now().Add(10*24*time.Hour))
	d := Decide([]string{"a.com"}, b, renewBefore, time.Now())
	if d.Action != ActionRenew {
		t.Fatalf("剩余不足应 renew,实际 %s(%s)", d.Action, d.Reason)
	}
}

func TestDecideExpired(t *testing.T) {
	b := bundle([]string{"a.com"}, time.Now().Add(-time.Hour))
	d := Decide([]string{"a.com"}, b, renewBefore, time.Now())
	if d.Action != ActionRenew {
		t.Fatalf("已过期应 renew,实际 %s", d.Action)
	}
}

func TestDecideSANChanged(t *testing.T) {
	b := bundle([]string{"a.com"}, time.Now().Add(60*24*time.Hour))
	d := Decide([]string{"a.com", "www.a.com"}, b, renewBefore, time.Now())
	if d.Action != ActionReissue {
		t.Fatalf("SAN 变化应 reissue,实际 %s", d.Action)
	}
	// 顺序无关
	b2 := bundle([]string{"b.com", "a.com"}, time.Now().Add(60*24*time.Hour))
	d2 := Decide([]string{"a.com", "b.com"}, b2, renewBefore, time.Now())
	if d2.Action != ActionSkip {
		t.Fatalf("相同 SAN 不同顺序应 skip,实际 %s", d2.Action)
	}
}

func TestDecideRetryBackoff(t *testing.T) {
	now := time.Now()
	b := bundle([]string{"a.com"}, time.Now().Add(60*24*time.Hour))
	b.Meta.FailCount = 2
	b.Meta.NextRetryAt = now.Add(time.Hour)

	// 未到重试时间 -> skip
	d := Decide([]string{"a.com"}, b, renewBefore, now)
	if d.Action != ActionSkip {
		t.Fatalf("退避中应 skip,实际 %s", d.Action)
	}
	// 已到重试时间 -> retry
	b.Meta.NextRetryAt = now.Add(-time.Minute)
	d = Decide([]string{"a.com"}, b, renewBefore, now)
	if d.Action != ActionRetry {
		t.Fatalf("到重试时间应 retry,实际 %s", d.Action)
	}
}

func TestNextRetryAfter(t *testing.T) {
	backoffs := []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour}
	cases := []struct {
		fail int
		want time.Duration
	}{
		{1, time.Hour},
		{2, 6 * time.Hour},
		{3, 24 * time.Hour},
		{4, 24 * time.Hour}, // 封顶
		{10, 24 * time.Hour},
	}
	for _, c := range cases {
		if got := NextRetryAfter(c.fail, backoffs); got != c.want {
			t.Errorf("fail=%d 期望 %v,实际 %v", c.fail, c.want, got)
		}
	}
	if got := NextRetryAfter(1, nil); got != time.Hour {
		t.Errorf("空退避序列应回退 1h,实际 %v", got)
	}
}
