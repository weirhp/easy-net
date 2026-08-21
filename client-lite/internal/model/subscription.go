package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	CurrentSubscriptionFileVersion = 2
	MaxClashSubscriptions          = 20
	MaxClashNodes                  = 800
)

type SubscriptionFile struct {
	Version       int            `json:"version"`
	Subscriptions []Subscription `json:"subscriptions"`
}

type Subscription struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	URL          string      `json:"url"`
	ListenPort   int         `json:"listenPort"`
	SelectedNode string      `json:"selectedNode,omitempty"`
	Active       bool        `json:"active,omitempty"`
	UpdatedAt    time.Time   `json:"updatedAt,omitempty"`
	Nodes        []ClashNode `json:"nodes"`
}

type ClashNode struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Server string         `json:"server,omitempty"`
	Port   int            `json:"port,omitempty"`
	Raw    map[string]any `json:"raw"`
}

func (s Subscription) Clone() Subscription {
	clone := s
	clone.Nodes = append([]ClashNode(nil), s.Nodes...)
	for i := range clone.Nodes {
		clone.Nodes[i].Raw = cloneMap(s.Nodes[i].Raw)
	}
	return clone
}

func (s *Subscription) Normalize() {
	s.ID = strings.TrimSpace(s.ID)
	s.Name = strings.TrimSpace(s.Name)
	s.URL = strings.TrimSpace(s.URL)
	s.SelectedNode = strings.TrimSpace(s.SelectedNode)
	valid := make([]ClashNode, 0, len(s.Nodes))
	seen := map[string]struct{}{}
	for _, node := range s.Nodes {
		node.Name = strings.TrimSpace(node.Name)
		node.Type = strings.TrimSpace(node.Type)
		node.Server = strings.TrimSpace(node.Server)
		if node.Name == "" || node.Raw == nil {
			continue
		}
		if _, exists := seen[node.Name]; exists {
			continue
		}
		seen[node.Name] = struct{}{}
		valid = append(valid, node)
		if len(valid) >= MaxClashNodes {
			break
		}
	}
	s.Nodes = valid
	if s.SelectedNode != "" {
		if _, ok := seen[s.SelectedNode]; !ok {
			s.SelectedNode = ""
		}
	}
}

func (s Subscription) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("订阅 ID 不能为空")
	}
	if s.Name == "" || len([]rune(s.Name)) > 40 {
		return fmt.Errorf("订阅名称无效")
	}
	if s.URL == "" {
		return fmt.Errorf("订阅地址不能为空")
	}
	if s.ListenPort < 1 || s.ListenPort > 65535 {
		return fmt.Errorf("订阅本地端口无效")
	}
	return nil
}

func (s Subscription) Node(name string) (ClashNode, bool) {
	name = strings.TrimSpace(name)
	for _, node := range s.Nodes {
		if node.Name == name {
			return node, true
		}
	}
	return ClashNode{}, false
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
