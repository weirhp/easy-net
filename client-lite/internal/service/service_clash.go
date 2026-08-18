package service

import (
	"fmt"
	"time"

	"easy-net/client-lite/internal/clashsub"
	"easy-net/client-lite/internal/model"
)

func (s *Service) AttachClash(manager *clashsub.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clash = manager
}

func (s *Service) ClashViews() []clashsub.View {
	if s.clash == nil {
		return nil
	}
	views := make([]clashsub.View, 0)
	for _, sub := range s.clash.List() {
		view := clashsub.View{
			ID: sub.ID, Name: sub.Name, URL: sub.URL, SelectedNode: sub.SelectedNode,
			ListenAddress: fmt.Sprintf("127.0.0.1:%d", sub.ListenPort),
			ProfileID:     clashsub.ProfileID(sub.ID), Running: s.clash.Running(sub.ID),
		}
		if !sub.UpdatedAt.IsZero() {
			view.UpdatedAt = sub.UpdatedAt.Format(time.RFC3339)
		}
		for _, node := range sub.Nodes {
			view.Nodes = append(view.Nodes, clashsub.NodeView{Name: node.Name, Type: node.Type, Server: node.Server, Port: node.Port})
		}
		if profile, ok := s.Profile(view.ProfileID); ok {
			view.ProfileDefault = profile.Default
		}
		s.mu.Lock()
		view.Error = s.errors[view.ProfileID]
		s.mu.Unlock()
		views = append(views, view)
	}
	return views
}

func (s *Service) ImportClash(name, rawURL string) (model.Subscription, error) {
	if s.clash == nil {
		return model.Subscription{}, fmt.Errorf("Clash 订阅不可用")
	}
	sub, err := s.clash.Import(name, rawURL, s.nextAvailablePort(17890))
	if err != nil {
		return model.Subscription{}, err
	}
	if err := s.upsertClashProfile(sub); err != nil {
		_ = s.clash.Delete(sub.ID)
		return model.Subscription{}, err
	}
	return sub, nil
}

func (s *Service) RefreshClash(id string) (model.Subscription, error) {
	if s.clash == nil {
		return model.Subscription{}, fmt.Errorf("Clash 订阅不可用")
	}
	wasRunning := s.clash.Running(id)
	sub, err := s.clash.Refresh(id)
	if err != nil {
		return model.Subscription{}, err
	}
	if err := s.upsertClashProfile(sub); err != nil {
		return model.Subscription{}, err
	}
	if wasRunning && sub.SelectedNode != "" {
		if err := s.StartClashNode(id, sub.SelectedNode); err != nil {
			return sub, err
		}
	}
	return sub, nil
}

func (s *Service) DeleteClash(id string) error {
	if s.clash == nil {
		return fmt.Errorf("Clash 订阅不可用")
	}
	if err := s.clash.Delete(id); err != nil {
		return err
	}
	_ = s.Delete(clashsub.ProfileID(id))
	return nil
}

func (s *Service) StartClashNode(id, nodeName string) error {
	if s.clash == nil {
		return fmt.Errorf("Clash 订阅不可用")
	}
	profileID := clashsub.ProfileID(id)
	if err := s.clash.StartNode(id, nodeName); err != nil {
		s.mu.Lock()
		s.errors[profileID] = err.Error()
		s.mu.Unlock()
		return err
	}
	sub, ok := s.clash.Get(id)
	if ok {
		if err := s.upsertClashProfile(sub); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.errors[profileID] = ""
	s.mu.Unlock()
	return nil
}

func (s *Service) SetClashNodeDefault(id, nodeName string, enabled bool) error {
	if !enabled {
		return s.SetDefault(clashsub.ProfileID(id), false)
	}
	if err := s.StartClashNode(id, nodeName); err != nil {
		return err
	}
	return s.SetDefault(clashsub.ProfileID(id), true)
}

func (s *Service) upsertClashProfile(sub model.Subscription) error {
	profile := model.Profile{
		ID: clashsub.ProfileID(sub.ID), Name: sub.Name, Type: model.ProxyTypeClash,
		ListenHost: "127.0.0.1", ListenPort: sub.ListenPort,
		Clash: &model.ClashConfig{SubscriptionID: sub.ID, NodeName: sub.SelectedNode},
	}
	if existing, ok := s.Profile(profile.ID); ok {
		profile.Default = existing.Default
	}
	return s.Upsert(profile, SecretValues{})
}

func (s *Service) clashRunningLocked(profile model.Profile) bool {
	if s.clash == nil || profile.Type != model.ProxyTypeClash || profile.Clash == nil {
		return false
	}
	return s.clash.Running(profile.Clash.SubscriptionID)
}

func (s *Service) startClash(profile model.Profile) error {
	if s.clash == nil {
		return fmt.Errorf("Clash 订阅不可用")
	}
	nodeName := ""
	if profile.Clash != nil {
		nodeName = profile.Clash.NodeName
	}
	if nodeName == "" && profile.Clash != nil {
		if sub, ok := s.clash.Get(profile.Clash.SubscriptionID); ok {
			nodeName = sub.SelectedNode
		}
	}
	if nodeName == "" {
		return fmt.Errorf("请先选择要启动的订阅节点")
	}
	return s.clash.StartNode(profile.Clash.SubscriptionID, nodeName)
}

func (s *Service) stopClash(profile model.Profile) {
	if s.clash == nil || profile.Clash == nil {
		return
	}
	_ = s.clash.Stop(profile.Clash.SubscriptionID)
}
