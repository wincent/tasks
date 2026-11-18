package git

import "time"

// Opt-in, handle-local diagnostics for configuration operation regressions. The
// repository mutex protects these counters; ordinary handles leave work nil.
// Durations overlap: graph traversal includes capture of included files, and
// captures include parse misses. They must not be added as independent phases.
type configurationWork struct {
	captures, reads, evaluations, copies, assemblies, nativeRecords, paths uint64
	captureTime, evaluationTime, assemblyTime                              time.Duration
}
