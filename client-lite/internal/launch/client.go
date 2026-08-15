package launch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func StartOnExisting(baseURL, id string) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	id = strings.TrimSpace(id)
	if baseURL == "" || id == "" {
		return fmt.Errorf("启动入口参数不完整")
	}
	client := &http.Client{
		Timeout:   8 * time.Second,
		Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext},
	}
	stateResponse, err := client.Get(baseURL + "/api/state")
	if err != nil {
		return fmt.Errorf("连接已运行的 Easy-Net Lite：%w", err)
	}
	defer stateResponse.Body.Close()
	if stateResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("已运行的 Easy-Net Lite 无法提供管理令牌")
	}
	var state struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(stateResponse.Body, 256*1024)).Decode(&state); err != nil || state.Token == "" {
		return fmt.Errorf("读取已运行的 Easy-Net Lite 令牌失败")
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/launches/"+id+"/start", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Easy-Net-Token", state.Token)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("通知已运行的 Easy-Net Lite 启动应用：%w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if response.StatusCode != http.StatusOK {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &payload)
		if payload.Error != "" {
			return fmt.Errorf("%s", payload.Error)
		}
		return fmt.Errorf("启动应用失败（%d）", response.StatusCode)
	}
	return nil
}

func AppsURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/#apps"
}
