package metrics

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
)

// DBStatsReader is the subset of *sql.DB consumed by DBPoolCollector. It is
// an interface so tests can drive the collector with a fixed DBStats snapshot
// instead of a real database pool.
type DBStatsReader interface {
	Stats() sql.DBStats
}

// DBPoolCollector exports the Go database connection-pool state. The pool
// statistics are read lazily on every scrape, so the gauges always reflect the
// pool health at scrape time instead of the state when the process started.
// The wait counters are emitted as CounterFunc-backed counters so PromQL
// rate()/increase() remain valid over the cumulative sql.DBStats fields.
type DBPoolCollector struct {
	pool        DBStatsReader
	maxOpen     prometheus.Gauge
	open        prometheus.Gauge
	inUse       prometheus.Gauge
	idle        prometheus.Gauge
	waitCount   prometheus.CounterFunc
	waitSeconds prometheus.CounterFunc
}

// NewDBPoolCollector returns a collector over the supplied pool. A nil pool is
// tolerated: the collector stays registered and reports zeroed values.
func NewDBPoolCollector(pool DBStatsReader) *DBPoolCollector {
	c := &DBPoolCollector{pool: pool}
	c.maxOpen = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "db_conn_max_open",
		Help:      "Maximum number of open database connections allowed by the pool.",
	})
	c.open = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "db_conn_open",
		Help:      "Number of established database connections in the pool.",
	})
	c.inUse = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "db_conn_in_use",
		Help:      "Number of database connections currently in use.",
	})
	c.idle = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "db_conn_idle",
		Help:      "Number of idle database connections in the pool.",
	})
	c.waitCount = prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "db_conn_wait_count_total",
		Help:      "Total number of connections waited for because the pool was full.",
	}, func() float64 {
		if c.pool == nil {
			return 0
		}
		return float64(c.pool.Stats().WaitCount)
	})
	c.waitSeconds = prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "db_conn_wait_seconds_total",
		Help:      "Total time spent waiting for a database connection from the pool.",
	}, func() float64 {
		if c.pool == nil {
			return 0
		}
		return c.pool.Stats().WaitDuration.Seconds()
	})
	return c
}

// Describe sends the descriptors for every metric the collector emits.
func (c *DBPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.maxOpen.Desc()
	ch <- c.open.Desc()
	ch <- c.inUse.Desc()
	ch <- c.idle.Desc()
	ch <- c.waitCount.Desc()
	ch <- c.waitSeconds.Desc()
}

// Collect samples the pool and emits the current state.
func (c *DBPoolCollector) Collect(ch chan<- prometheus.Metric) {
	stats := sql.DBStats{}
	if c.pool != nil {
		stats = c.pool.Stats()
	}
	c.maxOpen.Set(float64(stats.MaxOpenConnections))
	c.open.Set(float64(stats.OpenConnections))
	c.inUse.Set(float64(stats.InUse))
	c.idle.Set(float64(stats.Idle))
	c.maxOpen.Collect(ch)
	c.open.Collect(ch)
	c.inUse.Collect(ch)
	c.idle.Collect(ch)
	c.waitCount.Collect(ch)
	c.waitSeconds.Collect(ch)
}
