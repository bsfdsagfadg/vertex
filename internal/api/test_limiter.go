package api

import "context"

const sharedTestSessionLimit = 20

var testSessionSlots = make(chan struct{}, sharedTestSessionLimit)

func acquireTestSession(ctx context.Context) bool {
	select {
	case testSessionSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func releaseTestSession() {
	select {
	case <-testSessionSlots:
	default:
	}
}
