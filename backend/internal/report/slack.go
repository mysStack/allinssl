package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SlackReport struct {
	webhookURL string
	client     *http.Client
}

func NewSlackReport(webhookURL string) *SlackReport {
	return newSlackReport(webhookURL, &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
}

func newSlackReport(webhookURL string, client *http.Client) *SlackReport {
	return &SlackReport{webhookURL: webhookURL, client: client}
}

func (s *SlackReport) SendText(message string) error {
	if !isSlackWebhookURL(s.webhookURL) {
		return fmt.Errorf("Slack Webhook 地址无效")
	}

	payload, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: message})
	if err != nil {
		return fmt.Errorf("构建 Slack 消息失败")
	}

	request, err := http.NewRequest(http.MethodPost, s.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("Slack Webhook 地址无效")
	}
	request.Header.Set("Content-Type", "application/json")

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("请求 Slack 失败")
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Slack 返回 HTTP 状态 %d", response.StatusCode)
	}
	return nil
}

func NotifySlack(params map[string]any) error {
	if params == nil {
		return fmt.Errorf("缺少参数")
	}
	providerID, ok := params["provider_id"].(string)
	if !ok || strings.TrimSpace(providerID) == "" {
		return fmt.Errorf("缺少通知渠道")
	}
	subject, ok := params["subject"].(string)
	if !ok {
		return fmt.Errorf("通知标题错误")
	}
	body, ok := params["body"].(string)
	if !ok {
		return fmt.Errorf("通知内容错误")
	}

	providerData, err := GetReport(providerID)
	if err != nil {
		return err
	}
	configString, ok := providerData["config"].(string)
	if !ok {
		return fmt.Errorf("Slack 配置错误")
	}
	var config struct {
		Webhook string `json:"webhook"`
	}
	if err := json.Unmarshal([]byte(configString), &config); err != nil {
		return fmt.Errorf("解析 Slack 配置失败")
	}

	if err := NewSlackReport(config.Webhook).SendText(fmt.Sprintf("%s : %s", subject, body)); err != nil {
		return fmt.Errorf("Slack 发送失败: %w", err)
	}
	return nil
}

func isSlackWebhookURL(rawURL string) bool {
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host != "hooks.slack.com" || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return false
	}

	parts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "services" {
		return false
	}
	for _, part := range parts[1:] {
		if part == "" {
			return false
		}
	}
	return true
}
