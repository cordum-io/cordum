//go:build linux

package runtime

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func (w *Worker) sampleCPULoad() float32 {
	total, idle, ok := readProcCPU()
	if !ok {
		return 0
	}
	if w.cpuLastTotal == 0 {
		w.cpuLastTotal = total
		w.cpuLastIdle = idle
		return 0
	}
	deltaTotal := total - w.cpuLastTotal
	deltaIdle := idle - w.cpuLastIdle
	w.cpuLastTotal = total
	w.cpuLastIdle = idle
	if deltaTotal == 0 {
		return 0
	}
	usage := 1 - (float64(deltaIdle) / float64(deltaTotal))
	return clampPercent(usage * 100)
}

func (w *Worker) sampleMemoryLoad() float32 {
	total, available, ok := readProcMeminfo()
	if !ok || total == 0 {
		return 0
	}
	if available > total {
		available = total
	}
	used := total - available
	return clampPercent(float64(used) / float64(total) * 100)
}

func readProcCPU() (total uint64, idle uint64, ok bool) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}

	var nums []uint64
	for _, field := range fields[1:] {
		val, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		nums = append(nums, val)
		total += val
	}
	if len(nums) > 3 {
		idle = nums[3]
	}
	if len(nums) > 4 {
		idle += nums[4]
	}
	return total, idle, true
}

func readProcMeminfo() (total uint64, available uint64, ok bool) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()

	var memFree, buffers, cached uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value
		case "MemAvailable":
			available = value
		case "MemFree":
			memFree = value
		case "Buffers":
			buffers = value
		case "Cached":
			cached = value
		}
	}
	if total == 0 {
		return 0, 0, false
	}
	if available == 0 {
		available = memFree + buffers + cached
	}
	return total, available, true
}

func clampPercent(value float64) float32 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return float32(value)
}
