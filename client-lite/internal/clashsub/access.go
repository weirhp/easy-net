package clashsub

import (
	"context"
	"strings"
	"sync"
	"time"

	"easy-net/client-lite/internal/model"
)

const (
	accessTimeout = 8 * time.Second
	accessUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

type accessTarget struct {
	ID   string
	Name string
	Host string
	Path string
}

var accessTargets = []accessTarget{
	{ID: "gemini", Name: "Gemini", Host: "gemini.google.com", Path: "/"},
	{ID: "chatgpt", Name: "ChatGPT", Host: "chatgpt.com", Path: "/"},
	{ID: "grok", Name: "Grok", Host: "grok.com", Path: "/"},
	{ID: "claude", Name: "Claude", Host: "claude.ai", Path: "/"},
}

type SiteResult struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	LatencyMs int    `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (m *Manager) TestAccess(id, nodeName string) ([]NodeMetric, error) {
	nodes, err := m.nodesForTest(id, nodeName)
	if err != nil {
		return nil, err
	}
	sub, _ := m.Get(id)
	return runNodeTests(nodes, 2, func(node model.ClashNode) NodeMetric {
		metric := NodeMetric{Name: node.Name}
		err := m.withNodeSOCKS(sub, node, func(socksAddr string) error {
			metric.Sites = probeAccessTargets(socksAddr)
			return nil
		})
		if err != nil {
			metric.Error = "探测失败"
			metric.Sites = failedSiteResults("失败")
		}
		return metric
	}), nil
}

func SOCKSAccess(socksAddr string) []SiteResult {
	return probeAccessTargets(socksAddr)
}

func probeAccessTargets(socksAddr string) []SiteResult {
	results := make([]SiteResult, len(accessTargets))
	var wg sync.WaitGroup
	for i, target := range accessTargets {
		wg.Add(1)
		go func(index int, item accessTarget) {
			defer wg.Done()
			results[index] = probeAccessTarget(socksAddr, item)
		}(i, target)
	}
	wg.Wait()
	return results
}

func probeAccessTarget(socksAddr string, target accessTarget) SiteResult {
	ctx, cancel := context.WithTimeout(context.Background(), accessTimeout)
	defer cancel()
	start := time.Now()
	status, _, err := socksHTTPSExchange(ctx, socksAddr, target.Host, 443, target.Path, accessUA, 4096, false)
	ms := int(time.Since(start).Milliseconds())
	if ms < 1 {
		ms = 1
	}
	result := SiteResult{ID: target.ID, Name: target.Name, LatencyMs: ms}
	if err != nil || status < 1 {
		result.Error = accessErrorText(err)
		return result
	}
	result.OK = true
	return result
}

func failedSiteResults(reason string) []SiteResult {
	results := make([]SiteResult, 0, len(accessTargets))
	for _, target := range accessTargets {
		results = append(results, SiteResult{ID: target.ID, Name: target.Name, Error: reason})
	}
	return results
}

func accessErrorText(err error) string {
	if err == nil {
		return "阻断"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "超时"
	case strings.Contains(msg, "handshake"), strings.Contains(msg, "tls"), strings.Contains(msg, "握手"):
		return "握手失败"
	default:
		return "阻断"
	}
}
