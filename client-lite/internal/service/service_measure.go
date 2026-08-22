package service

import (
	"context"
	"fmt"
	"net"
	"time"

	"easy-net/client-lite/internal/clashsub"
	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/proxy"
)

func (s *Service) TestProfileDelay(id string) ([]clashsub.NodeMetric, error) {
	profile, err := s.manualTestProfile(id)
	if err != nil {
		return nil, err
	}
	host, port, err := profile.DelayTarget()
	if err != nil {
		return nil, err
	}
	ms, err := clashsub.TCPDelay(host, port)
	metric := clashsub.NodeMetric{Name: profile.Name, DelayMs: ms}
	if err != nil {
		metric.DelayMs = 0
		metric.Error = "超时"
	}
	return []clashsub.NodeMetric{metric}, nil
}

func (s *Service) TestProfileSpeed(id string) ([]clashsub.NodeMetric, error) {
	profile, err := s.manualTestProfile(id)
	if err != nil {
		return nil, err
	}
	var mbps float64
	if err := s.withProfileSOCKS(profile, func(addr string) error {
		var measureErr error
		mbps, measureErr = clashsub.SOCKSSpeed(addr)
		return measureErr
	}); err != nil {
		return []clashsub.NodeMetric{{Name: profile.Name, Error: "失败"}}, nil
	}
	return []clashsub.NodeMetric{{Name: profile.Name, SpeedMbps: mbps}}, nil
}

func (s *Service) TestProfileAccess(id string) ([]clashsub.NodeMetric, error) {
	profile, err := s.manualTestProfile(id)
	if err != nil {
		return nil, err
	}
	var sites []clashsub.SiteResult
	if err := s.withProfileSOCKS(profile, func(addr string) error {
		sites = clashsub.SOCKSAccess(addr)
		return nil
	}); err != nil {
		return nil, err
	}
	return []clashsub.NodeMetric{{Name: profile.Name, Sites: sites}}, nil
}

func (s *Service) withProfileSOCKS(profile model.Profile, fn func(string) error) error {
	if profile.Type == model.ProxyTypeExternal || s.profileRunning(profile.ID) {
		return fn(profile.ListenAddress())
	}
	outbound, err := s.buildTransport(profile)
	if err != nil {
		return err
	}
	addr, err := freeLocalSOCKSAddr()
	if err != nil {
		return err
	}
	server := proxy.NewServer(addr, outbound, nil)
	server.SetBypassPrivate(profile.BypassPrivate)
	server.SetBypassChina(profile.BypassChina)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		return err
	}
	defer server.Stop()
	return fn(addr)
}

func (s *Service) manualTestProfile(id string) (model.Profile, error) {
	profile, ok := s.Profile(id)
	if !ok {
		return model.Profile{}, fmt.Errorf("代理配置不存在")
	}
	if profile.Type == model.ProxyTypeClash {
		return model.Profile{}, fmt.Errorf("Clash 节点请在订阅列表中测试")
	}
	return profile, nil
}

func (s *Service) profileRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	server := s.instances[id]
	return server != nil && server.Running()
}

func freeLocalSOCKSAddr() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr, nil
}
