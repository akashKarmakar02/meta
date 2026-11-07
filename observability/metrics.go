package observability

import (
	"sort"
	"sync"
	"time"
)

// Metric aggregates counts and total duration without request-specific labels.
type Metric struct {
	Kind      string        `json:"kind"`
	Route     string        `json:"route"`
	Operation string        `json:"operation,omitempty"`
	Outcome   string        `json:"outcome"`
	Status    int           `json:"status,omitempty"`
	Count     uint64        `json:"count"`
	Duration  time.Duration `json:"duration_ns"`
}

type metricKey struct {
	kind, route, operation, outcome string
	status                          int
}

// Collector is a concurrency-safe in-memory metrics sink. Use one per process.
type Collector struct {
	mu     sync.Mutex
	values map[metricKey]Metric
}

// NewCollector creates an empty collector.
func NewCollector() *Collector { return &Collector{values: make(map[metricKey]Metric)} }

// Observe records an event.
func (c *Collector) Observe(e Event) {
	if c == nil {
		return
	}
	key := metricKey{e.Kind, e.Route, e.Operation, e.Outcome, e.Status}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[metricKey]Metric)
	}
	m := c.values[key]
	m.Kind = e.Kind
	m.Route = e.Route
	m.Operation = e.Operation
	m.Outcome = e.Outcome
	m.Status = e.Status
	m.Count++
	m.Duration += e.Duration
	c.values[key] = m
}

// Snapshot returns an independent, deterministically ordered copy.
func (c *Collector) Snapshot() []Metric {
	if c == nil {
		return []Metric{}
	}
	c.mu.Lock()
	result := make([]Metric, 0, len(c.values))
	for _, m := range c.values {
		result = append(result, m)
	}
	c.mu.Unlock()
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.Outcome != b.Outcome {
			return a.Outcome < b.Outcome
		}
		return a.Status < b.Status
	})
	return result
}
