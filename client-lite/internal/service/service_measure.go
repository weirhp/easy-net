package service

import (
	"fmt"

	"easy-net/client-lite/internal/clashsub"
	"easy-net/client-lite/internal/model"
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
	addr, profile, err := s.profileTestSOCKS(id)
	if err != nil {
		return nil, err
	}
	mbps, err := clashsub.SOCKSSpeed(addr)
	metric := clashsub.NodeMetric{Name: profile.Name}
	if err != nil {
		metric.Error = "失败"
		return []clashsub.NodeMetric{metric}, nil
	}
	metric.SpeedMbps = mbps
	return []clashsub.NodeMetric{metric}, nil
}

func (s *Service) TestProfileAccess(id string) ([]clashsub.NodeMetric, error) {
	addr, profile, err := s.profileTestSOCKS(id)
	if err != nil {
		return nil, err
	}
	return []clashsub.NodeMetric{{Name: profile.Name, Sites: clashsub.SOCKSAccess(addr)}}, nil
}

func (s *Service) profileTestSOCKS(id string) (string, model.Profile, error) {
	profile, err := s.manualTestProfile(id)
	if err != nil {
		return "", model.Profile{}, err
	}
	if profile.Type != model.ProxyTypeExternal && !s.profileRunning(id) {
		return "", profile, fmt.Errorf("请先启动该代理")
	}
	return profile.ListenAddress(), profile, nil
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
