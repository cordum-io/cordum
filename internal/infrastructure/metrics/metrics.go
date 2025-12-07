package metrics

// Metrics defines counters for scheduler and workers.
type Metrics interface {
	IncJobsReceived(topic string)
	IncJobsDispatched(topic string)
	IncJobsCompleted(topic, status string)
	IncSafetyDenied(topic string)
}

// Noop implements Metrics without emitting anything.
type Noop struct{}

func (Noop) IncJobsReceived(string)              {}
func (Noop) IncJobsDispatched(string)            {}
func (Noop) IncJobsCompleted(string, string)     {}
func (Noop) IncSafetyDenied(string)              {}
