package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type TrafficCounter struct {
	UpBytes   uint64 `json:"upBytes"`
	DownBytes uint64 `json:"downBytes"`
}

type TrafficSnapshot struct {
	Day        string `json:"day"`
	UpBytes    uint64 `json:"upBytes"`
	DownBytes  uint64 `json:"downBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

type TrafficStats struct {
	path string

	mu       sync.Mutex
	day      string
	easyNet  map[string]TrafficCounter
	dirty    bool
	lastSave time.Time
}

type trafficStatsFile struct {
	Day     string                    `json:"day"`
	EasyNet map[string]TrafficCounter `json:"easyNet"`
}

func NewTrafficStats(workDir string) *TrafficStats {
	t := &TrafficStats{
		path:    filepath.Join(workDir, "traffic-stats.json"),
		day:     todayText(),
		easyNet: make(map[string]TrafficCounter),
	}
	t.load()
	t.rollDayLocked()
	return t
}

func (t *TrafficStats) AddEasyNet(id string, upBytes uint64, downBytes uint64) {
	if t == nil || id == "" || (upBytes == 0 && downBytes == 0) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.rollDayLocked()
	counter := t.easyNet[id]
	counter.UpBytes += upBytes
	counter.DownBytes += downBytes
	t.easyNet[id] = counter
	t.dirty = true

	if time.Since(t.lastSave) >= 5*time.Second {
		t.saveLocked()
	}
}

func (t *TrafficStats) SnapshotEasyNet(ids []string) map[string]TrafficSnapshot {
	result := make(map[string]TrafficSnapshot)
	if t == nil {
		return result
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.rollDayLocked()
	for _, id := range ids {
		counter := t.easyNet[id]
		result[id] = TrafficSnapshot{
			Day:        t.day,
			UpBytes:    counter.UpBytes,
			DownBytes:  counter.DownBytes,
			TotalBytes: counter.UpBytes + counter.DownBytes,
		}
	}
	return result
}

func (t *TrafficStats) Save() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.saveLocked()
}

func (t *TrafficStats) load() {
	data, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var file trafficStatsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return
	}
	if file.Day != "" {
		t.day = file.Day
	}
	if file.EasyNet != nil {
		t.easyNet = file.EasyNet
	}
}

func (t *TrafficStats) rollDayLocked() {
	day := todayText()
	if t.day == day {
		return
	}
	t.day = day
	t.easyNet = make(map[string]TrafficCounter)
	t.dirty = true
	t.saveLocked()
}

func (t *TrafficStats) saveLocked() {
	if !t.dirty {
		return
	}
	data, err := json.MarshalIndent(trafficStatsFile{
		Day:     t.day,
		EasyNet: t.easyNet,
	}, "", "  ")
	if err == nil {
		_ = os.WriteFile(t.path, append(data, '\n'), 0644)
	}
	t.lastSave = time.Now()
	t.dirty = false
}

func todayText() string {
	return time.Now().Format("2006-01-02")
}
