package clashsub

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxSubscriptionBytes = 8 << 20

func Fetch(rawURL string) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("订阅地址无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("订阅地址必须使用 http:// 或 https://")
	}
	request, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建订阅请求：%w", err)
	}
	request.Header.Set("User-Agent", "clash.meta")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载 Clash 订阅：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("订阅服务器返回 HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSubscriptionBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取订阅内容：%w", err)
	}
	if len(data) > maxSubscriptionBytes {
		return nil, fmt.Errorf("订阅内容过大")
	}
	return data, nil
}
