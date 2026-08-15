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
	response, body, err := postLaunch(client, baseURL, id, state.Token, false)
	if err != nil {
		return fmt.Errorf("通知已运行的 Easy-Net Lite 启动应用：%w", err)
	}
	if response.StatusCode == http.StatusConflict {
		var payload struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(body, &payload)
		if payload.Code == "application_already_running" && ConfirmRunningApplication(payload.Name) {
			response, body, err = postLaunch(client, baseURL, id, state.Token, true)
			if err != nil {
				return fmt.Errorf("通知已运行的 Easy-Net Lite 启动应用：%w", err)
			}
		} else if payload.Code == "application_already_running" {
			return nil
		}
	}
	if response.StatusCode != http.StatusOK {
		var payload struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(body, &payload)
		if payload.Error != "" {
			title := "启动失败"
			if payload.Code == "proxy_unavailable" {
				title = "代理不可用"
			} else if payload.Code == "application_not_running" {
				title = "接管失败"
			}
			ShowLaunchError(title, payload.Error)
			return nil
		}
		return fmt.Errorf("启动应用失败（%d）", response.StatusCode)
	}
	return nil
}

func postLaunch(client *http.Client, baseURL, id, token string, confirmRunning bool) (*http.Response, []byte, error) {
	payload, _ := json.Marshal(map[string]bool{"confirmRunning": confirmRunning})
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/launches/"+id+"/start", bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Easy-Net-Token", token)
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	response.Body.Close()
	if readErr != nil {
		return nil, nil, readErr
	}
	return response, body, nil
}

func AppsURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/#apps"
}
