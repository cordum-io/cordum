//go:build !linux

package runtime

func (w *Worker) sampleCPULoad() float32 {
	return 0
}

func (w *Worker) sampleMemoryLoad() float32 {
	return 0
}
