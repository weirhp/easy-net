package clashsub

import (
	"context"
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
	client := &http.Client{
		Timeout: 18 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("订阅重定向次数过多")
			}
			request.Header.Set("User-Agent", "clash.meta")
			return nil
		},
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 350 * time.Millisecond)
		}
		requestContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		request, requestErr := http.NewRequestWithContext(requestContext, http.MethodGet, parsed.String(), nil)
		if requestErr != nil {
			cancel()
			return nil, fmt.Errorf("创建订阅请求：%w", requestErr)
		}
		request.Header.Set("User-Agent", "clash.meta")
		request.Header.Set("Accept", "application/yaml,text/yaml,text/plain,application/octet-stream,*/*")
		response, requestErr := client.Do(request)
		if requestErr != nil {
			cancel()
			last = requestErr
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, maxSubscriptionBytes+1))
		_ = response.Body.Close()
		cancel()
		if response.StatusCode >= 200 && response.StatusCode < 300 && readErr == nil {
			if len(data) > maxSubscriptionBytes {
				return nil, fmt.Errorf("订阅内容过大")
			}
			if len(data) == 0 {
				last = fmt.Errorf("订阅服务器返回了空内容")
				continue
			}
			return data, nil
		}
		if readErr != nil {
			last = fmt.Errorf("读取订阅内容：%w", readErr)
		} else {
			last = fmt.Errorf("订阅服务器返回 HTTP %d", response.StatusCode)
		}
		if response.StatusCode > 0 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
			break
		}
	}
	return nil, fmt.Errorf("下载 Clash 订阅失败（已重试）：%w", last)
}
