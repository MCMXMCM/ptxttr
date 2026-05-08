package httpx

import (
	"sync"
	"time"
)

type appMetrics struct {
	mu       sync.Mutex
	counters map[string]int64
	timings  map[string]timingMetric
}

type timingMetric struct {
	Count int64         `json:"count"`
	Total time.Duration `json:"total"`
	Max   time.Duration `json:"max"`
}

func newAppMetrics() *appMetrics {
	return &appMetrics{
		counters: make(map[string]int64),
		timings:  make(map[string]timingMetric),
	}
}

func (m *appMetrics) Add(name string, delta int64) {
	if m == nil || name == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name] += delta
}

func (m *appMetrics) Observe(name string, duration time.Duration) {
	if m == nil || name == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	timing := m.timings[name]
	timing.Count++
	timing.Total += duration
	if duration > timing.Max {
		timing.Max = duration
	}
	m.timings[name] = timing
}

func (m *appMetrics) Snapshot() map[string]any {
	if m == nil {
		return map[string]any{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	counters := make(map[string]int64, len(m.counters))
	for key, value := range m.counters {
		counters[key] = value
	}
	timings := make(map[string]map[string]any, len(m.timings))
	for key, value := range m.timings {
		avg := time.Duration(0)
		if value.Count > 0 {
			avg = value.Total / time.Duration(value.Count)
		}
		timings[key] = map[string]any{
			"count": value.Count,
			"total": value.Total.String(),
			"avg":   avg.String(),
			"max":   value.Max.String(),
		}
	}
	return map[string]any{
		"counters": counters,
		"timings":  timings,
	}
}

func (s *Server) observe(name string, started time.Time) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.Observe(name, time.Since(started))
}
