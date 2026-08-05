package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go-zs/internal/certmgr"
)

// Webhook 把证书更新事件 POST 到指定 URL,由网关自行热加载。
type Webhook struct {
	url    string
	client *http.Client
}

func NewWebhookDeployer(url string) (*Webhook, error) {
	if url == "" {
		return nil, errors.New("webhook 部署需要 url")
	}
	return &Webhook{url: url, client: &http.Client{Timeout: 15 * time.Second}}, nil
}

func (w *Webhook) Name() string { return "webhook" }

func (w *Webhook) Deploy(ctx context.Context, bundle *certmgr.Bundle) error {
	if bundle == nil {
		return errors.New("证书包为空")
	}
	if bundle.Meta.DeployedFingerprint == bundle.Fingerprint &&
		contains(bundle.Meta.DeployedTargets, w.Name()) {
		return nil
	}
	payload := map[string]interface{}{
		"event":       "cert.updated",
		"name":        bundle.Name,
		"domains":     bundle.Domains,
		"not_after":   bundle.NotAfter.Format(time.RFC3339),
		"fingerprint": bundle.Fingerprint,
		"issuer":      bundle.Issuer,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook 推送失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook 返回状态 %d", resp.StatusCode)
	}
	return nil
}
