package shadow

import (
	"context"

	"github.com/shirou/gopsutil/v3/process"
)

// defaultProcessLister is the production process enumerator. It returns
// the (name, pid) tuples of every visible process via
// github.com/shirou/gopsutil/v3/process. Errors enumerating individual
// processes are silenced — partial process visibility is normal on
// unprivileged hosts and is preferable to failing the whole scan.
func defaultProcessLister() ([]ProcessInfo, error) {
	ctx := context.Background()
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProcessInfo, 0, len(procs))
	for _, p := range procs {
		name, err := p.NameWithContext(ctx)
		if err != nil {
			// One-by-one read failures are routine for processes that
			// exit between enumeration and inspection; skip silently.
			continue
		}
		out = append(out, ProcessInfo{Name: name, PID: p.Pid})
	}
	return out, nil
}
