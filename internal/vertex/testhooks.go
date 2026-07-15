package vertex

import "github.com/bsfdsagfadg/vertex/internal/recaptcha"

// SetBatchGraphqlURL overrides the batchGraphql URL for testing.
// Will be replaced by dependency injection in phase 3/4.
func SetBatchGraphqlURL(url string) {
	batchGraphqlURL = url
}

// SetTokenPool replaces the token pool for testing.
// Will be replaced by dependency injection in phase 3/4.
func (c *VertexAIClient) SetTokenPool(pool *recaptcha.TokenPool) {
	c.pool = pool
}

// testHookCollectorDone is closed when the background collector exits.
// Set via SetCollectorDoneHook before calling RunRace with noCancelOnSuccess.
// NOT safe for concurrent use across parallel tests.
var testHookCollectorDone chan struct{} //nolint:gochecknoglobals

// SetCollectorDoneHook registers a channel that the background collector
// closes upon exit. Pass nil to clear.
func SetCollectorDoneHook(ch chan struct{}) {
	testHookCollectorDone = ch
}
