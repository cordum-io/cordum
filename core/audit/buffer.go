package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultBatchSize    = 100
	defaultFlushInterval = 5 * time.Second
	defaultChanSize     = 1000
	defaultExportTimeout = 10 * time.Second
	maxRetries          = 3
)

// BufferedExporter wraps an Exporter with async batching and retry.
type BufferedExporter struct {
	exporter Exporter
	ch       chan SIEMEvent
	done     chan struct{}
	wg       sync.WaitGroup

	batchSize     int
	flushInterval time.Duration
}

// BufferOption configures a BufferedExporter.
type BufferOption func(*BufferedExporter)

// WithBatchSize sets the maximum batch size before flushing.
func WithBatchSize(n int) BufferOption {
	return func(b *BufferedExporter) { b.batchSize = n }
}

// WithFlushInterval sets the maximum time between flushes.
func WithFlushInterval(d time.Duration) BufferOption {
	return func(b *BufferedExporter) { b.flushInterval = d }
}

// NewBufferedExporter wraps an Exporter with async batching.
func NewBufferedExporter(exp Exporter, opts ...BufferOption) *BufferedExporter {
	b := &BufferedExporter{
		exporter:      exp,
		ch:            make(chan SIEMEvent, defaultChanSize),
		done:          make(chan struct{}),
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
	}
	for _, o := range opts {
		o(b)
	}
	b.wg.Add(1)
	go b.loop()
	return b
}

// Send enqueues an event for export. Non-blocking; drops if buffer is full.
func (b *BufferedExporter) Send(event SIEMEvent) {
	select {
	case b.ch <- event:
	default:
		slog.Warn("audit exporter buffer full, dropping event",
			"event_type", event.EventType,
			"action", event.Action,
		)
	}
}

// Close drains remaining events and shuts down the exporter.
func (b *BufferedExporter) Close() error {
	close(b.done)
	b.wg.Wait()
	return b.exporter.Close()
}

func (b *BufferedExporter) loop() {
	defer b.wg.Done()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-b.done
		cancel()
	}()

	batch := make([]SIEMEvent, 0, b.batchSize)
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		toSend := make([]SIEMEvent, len(batch))
		copy(toSend, batch)
		batch = batch[:0]
		b.exportWithRetry(ctx, toSend)
	}

	for {
		select {
		case ev, ok := <-b.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, ev)
			if len(batch) >= b.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-b.done:
			// Drain remaining events from channel.
			for {
				select {
				case ev := <-b.ch:
					batch = append(batch, ev)
					if len(batch) >= b.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (b *BufferedExporter) exportWithRetry(ctx context.Context, events []SIEMEvent) {
	backoff := time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		exportCtx, cancel := context.WithTimeout(ctx, defaultExportTimeout)
		err := b.exporter.Export(exportCtx, events)
		cancel()
		if err != nil {
			slog.Error("audit export failed",
				"attempt", attempt+1,
				"events", len(events),
				"error", err,
			)
			if attempt < maxRetries-1 {
				select {
				case <-ctx.Done():
					slog.Warn("audit export cancelled during retry",
						"attempt", attempt+1,
						"events", len(events),
					)
					return
				case <-time.After(backoff):
				}
				backoff *= 2
			}
			continue
		}
		return
	}
	slog.Error("audit export failed after all retries, dropping batch",
		"events", len(events),
	)
}
