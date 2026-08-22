package clashsub

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy-net/client-lite/internal/model"
)

const (
	delayTimeout   = 2500 * time.Millisecond
	speedTestBytes = 256 * 1024
	speedTimeout   = 12 * time.Second
)

type NodeMetric struct {
	Name      string       `json:"name"`
	DelayMs   int          `json:"delayMs,omitempty"`
	SpeedMbps float64      `json:"speedMbps,omitempty"`
	Error     string       `json:"error,omitempty"`
	Sites     []SiteResult `json:"sites,omitempty"`
}

func (m *Manager) TestDelay(id, nodeName string) ([]NodeMetric, error) {
	nodes, err := m.nodesForTest(id, nodeName)
	if err != nil {
		return nil, err
	}
	return runNodeTests(nodes, 16, func(node model.ClashNode) NodeMetric {
		delay, err := tcpDelay(node.Server, node.Port, delayTimeout)
		metric := NodeMetric{Name: node.Name, DelayMs: delay}
		if err != nil {
			metric.DelayMs = 0
			metric.Error = "超时"
		}
		return metric
	}), nil
}

func (m *Manager) TestSpeed(id, nodeName string) ([]NodeMetric, error) {
	nodes, err := m.nodesForTest(id, nodeName)
	if err != nil {
		return nil, err
	}
	sub, _ := m.Get(id)
	return runNodeTests(nodes, 2, func(node model.ClashNode) NodeMetric {
		metric := NodeMetric{Name: node.Name}
		mbps, err := m.measureNodeSpeed(sub, node)
		if err != nil {
			metric.Error = "失败"
			return metric
		}
		metric.SpeedMbps = mbps
		return metric
	}), nil
}

func (m *Manager) nodesForTest(id, nodeName string) ([]model.ClashNode, error) {
	sub, ok := m.Get(id)
	if !ok {
		return nil, fmt.Errorf("Clash 订阅不存在")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		if len(sub.Nodes) == 0 {
			return nil, fmt.Errorf("订阅里没有节点")
		}
		return sub.Nodes, nil
	}
	node, ok := sub.Node(nodeName)
	if !ok {
		return nil, fmt.Errorf("订阅中找不到节点 %q", nodeName)
	}
	return []model.ClashNode{node}, nil
}

func (m *Manager) measureNodeSpeed(sub model.Subscription, node model.ClashNode) (float64, error) {
	var mbps float64
	err := m.withNodeSOCKS(sub, node, func(socksAddr string) error {
		var measureErr error
		mbps, measureErr = socksDownloadMbps(socksAddr, speedTimeout)
		return measureErr
	})
	return mbps, err
}

func (m *Manager) withNodeSOCKS(sub model.Subscription, node model.ClashNode, fn func(socksAddr string) error) error {
	if m.runner != nil && m.runner.Running(sub.ID) && sub.SelectedNode == node.Name && sub.ListenPort > 0 {
		return fn(net.JoinHostPort("127.0.0.1", strconv.Itoa(sub.ListenPort)))
	}
	return m.withTempNodeSOCKS(sub.ID, node, fn)
}

func (m *Manager) withTempNodeSOCKS(subscriptionID string, node model.ClashNode, fn func(socksAddr string) error) error {
	exe, err := findMihomo(m.dir)
	if err != nil {
		return err
	}
	port, err := freeLocalPort()
	if err != nil {
		return err
	}
	workDir := filepath.Join(m.dir, "clash-test", fmt.Sprintf("%s-%d", subscriptionID, port))
	configPath := filepath.Join(workDir, "config.yaml")
	if err := WriteMihomoConfig(configPath, port, node.Raw, false, false); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(workDir, "mihomo.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "-d", workDir, "-f", configPath)
	cmd.Dir = workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = withoutProxyEnv(os.Environ())
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	control, err := retainProcessControl(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		_ = control.Terminate()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
		control.Close()
		_ = logFile.Close()
		_ = os.RemoveAll(workDir)
	}()
	socksAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := waitPort(socksAddr, 8*time.Second); err != nil {
		return err
	}
	return fn(socksAddr)
}

func runNodeTests(nodes []model.ClashNode, workers int, fn func(model.ClashNode) NodeMetric) []NodeMetric {
	if workers < 1 {
		workers = 1
	}
	results := make([]NodeMetric, len(nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, node := range nodes {
		wg.Add(1)
		sem <- struct{}{}
		go func(index int, item model.ClashNode) {
			defer wg.Done()
			defer func() { <-sem }()
			results[index] = fn(item)
		}(i, node)
	}
	wg.Wait()
	return results
}

func TCPDelay(host string, port int) (int, error) {
	return tcpDelay(host, port, delayTimeout)
}

func SOCKSSpeed(socksAddr string) (float64, error) {
	return socksDownloadMbps(socksAddr, speedTimeout)
}

func tcpDelay(host string, port int, timeout time.Duration) (int, error) {
	host = strings.TrimSpace(host)
	if host == "" || port < 1 || port > 65535 {
		return 0, fmt.Errorf("节点地址无效")
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	ms := int(time.Since(start).Milliseconds())
	if ms < 1 {
		ms = 1
	}
	return ms, nil
}

func socksDownloadMbps(socksAddr string, timeout time.Duration) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	n, err := socksHTTPSGet(ctx, socksAddr, "speed.cloudflare.com", 443, "/__down?bytes="+strconv.Itoa(speedTestBytes))
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	mbps := (float64(n) * 8) / elapsed / 1_000_000
	if mbps < 0.01 {
		return 0, fmt.Errorf("吞吐过低")
	}
	return mbps, nil
}

func socksHTTPSGet(ctx context.Context, socksAddr, host string, port int, path string) (int, error) {
	_, n, err := socksHTTPSExchange(ctx, socksAddr, host, port, path, "Easy-Net-Lite", speedTestBytes, true)
	return n, err
}

func socksHTTPSExchange(ctx context.Context, socksAddr, host string, port int, path, userAgent string, maxBody int, requireOK bool) (int, int, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", socksAddr)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return 0, 0, err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 5 || reply[1] != 0 {
		return 0, 0, fmt.Errorf("SOCKS5 握手失败")
	}
	target := []byte(host)
	request := []byte{5, 1, 0, 3, byte(len(target))}
	request = append(request, target...)
	request = append(request, byte(port>>8), byte(port))
	if _, err := conn.Write(request); err != nil {
		return 0, 0, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 5 || head[1] != 0 {
		return 0, 0, fmt.Errorf("节点未能建立目标连接")
	}
	if err := discardSOCKSAddress(conn, head[3]); err != nil {
		return 0, 0, err
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return 0, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort(host, strconv.Itoa(port))+path, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Host = host
	if userAgent == "" {
		userAgent = "Easy-Net-Lite"
	}
	req.Header.Set("User-Agent", userAgent)
	if err := req.Write(tlsConn); err != nil {
		return 0, 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if requireOK && (resp.StatusCode < 200 || resp.StatusCode >= 400) {
		return resp.StatusCode, 0, fmt.Errorf("测速返回 HTTP %d", resp.StatusCode)
	}
	if maxBody < 1 {
		maxBody = 4096
	}
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, int64(maxBody)))
	if err != nil && n == 0 {
		return resp.StatusCode, 0, err
	}
	return resp.StatusCode, int(n), nil
}

func freeLocalPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
