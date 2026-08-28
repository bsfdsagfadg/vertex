package api

import (
	"log"
	"runtime"
	"sync"
	"time"
)

// postTestGC coalesces cleanup requests from concurrently finishing test
// batches. The request is made only after all workers in that batch have
// exited, so GC is a final cleanup step rather than a per-node throttle.
var postTestGC struct {
	sync.Mutex
	pending bool
	lastRun time.Time
}

const postTestGCCooldown = 2 * time.Second

func requestPostTestGC(batchSize int) {
	postTestGC.Lock()
	if postTestGC.pending {
		postTestGC.Unlock()
		return
	}
	postTestGC.pending = true
	wait := time.Duration(0)
	if !postTestGC.lastRun.IsZero() {
		wait = postTestGCCooldown - time.Since(postTestGC.lastRun)
		if wait < 0 {
			wait = 0
		}
	}
	postTestGC.Unlock()
	go func() {
		if wait > 0 {
			time.Sleep(wait)
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		runtime.GC()
		runtime.ReadMemStats(&after)
		log.Printf("[Admin] test batch GC: nodes=%d heap_alloc=%d->%d heap_inuse=%d->%d num_gc=%d->%d", batchSize, before.HeapAlloc, after.HeapAlloc, before.HeapInuse, after.HeapInuse, before.NumGC, after.NumGC)
		postTestGC.Lock()
		postTestGC.pending = false
		postTestGC.lastRun = time.Now()
		postTestGC.Unlock()
	}()
}
