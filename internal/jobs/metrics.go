package jobs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics records each finished job for Prometheus.
type Metrics struct {
	duration *prometheus.HistogramVec
	cards    *prometheus.CounterVec
}

// NewMetrics registers the job metrics with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "innkeeper_job_duration_seconds",
			Help: "Time from a job's first claim to its finish, retries included.",
			// 0.1s to about 7h: jobs range from a quick push to a full archive import.
			Buckets: prometheus.ExponentialBuckets(0.1, 4, 10),
		}, []string{"kind", "status"}),
		cards: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "innkeeper_cards_changed_total",
			Help: "Cards finished jobs changed in the library or in an app.",
		}, []string{"kind"}),
	}
	reg.MustRegister(m.duration, m.cards)
	return m
}

// observe records a job that finished with status after changing cards.
func (m *Metrics) observe(j *Job, status string, cards int) {
	if j.StartedAt != nil {
		m.duration.WithLabelValues(j.Kind, status).Observe(time.Since(*j.StartedAt).Seconds())
	}
	m.cards.WithLabelValues(j.Kind).Add(float64(cards))
}
