package api

import (
	"runtime"

	"github.com/bsfdsagfadg/vertex/internal/infra/spool"
)

func metricsBody() map[string]any {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return map[string]any{
		"status": "ok",
		"memory": map[string]any{
			"alloc_mb":   bToMb(m.Alloc),
			"heap_inuse": bToMb(m.HeapInuse),
			"num_gc":     m.NumGC,
			"goroutines": runtime.NumGoroutine(),
			"spilled_mb": bToMb(uint64(spool.SpilledBytes())),
		},
	}
}

func bToMb(b uint64) float64 {
	return float64(b) / 1024 / 1024
}
