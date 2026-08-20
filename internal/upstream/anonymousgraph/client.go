package anonymousgraph

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const (
	BaseURL          = "https://cloudconsole-pa.clients6.google.com"
	BatchGraphQLPath = "/v3/entityServices/AiplatformEntityService/schemas/AIPLATFORM_GRAPHQL:batchGraphql"
	DefaultAPIKey    = "AIzaSyCI-zsRP85UVOi0DjtiCwWBwQ1djDy741g"
)

type RouteTokenSource interface {
	GetTokenWithRouteContext(context.Context, transport.Route) (string, error)
}

// Client owns only the anonymous Graph upstream wire boundary: route-bound
// sessions, reCAPTCHA affinity, endpoint/headers and streaming HTTP exchange.
// Scheduling and downstream protocol envelopes remain outside this package.
type Client struct {
	network          *transport.NetworkClient
	tokens           RouteTokenSource
	mu               sync.RWMutex
	endpointOverride string
}

func New(network *transport.NetworkClient, tokens RouteTokenSource) (*Client, error) {
	if network == nil {
		return nil, errors.New("anonymous graph network client is nil")
	}
	if tokens == nil {
		return nil, errors.New("anonymous graph token source is nil")
	}
	return &Client{network: network, tokens: tokens}, nil
}

func (c *Client) Network() *transport.NetworkClient { return c.network }

func (c *Client) SetEndpointOverride(endpoint string) {
	c.mu.Lock()
	c.endpointOverride = strings.TrimSpace(endpoint)
	c.mu.Unlock()
}

func (c *Client) OpenSession(ctx context.Context, timeoutSeconds int, requestNodeURI, requestID string) (*transport.Session, error) {
	return c.network.CreateSessionContext(ctx, timeoutSeconds, requestNodeURI, requestID)
}

func (c *Client) OpenRouteSession(timeoutSeconds int, route transport.Route, requestID string) (*transport.Session, error) {
	return c.network.CreateSessionRoute(timeoutSeconds, route, requestID)
}

func (c *Client) RouteToken(ctx context.Context, route transport.Route) (string, error) {
	return c.tokens.GetTokenWithRouteContext(ctx, route)
}

func (c *Client) DoStream(
	ctx context.Context,
	session *transport.Session,
	apiKey string,
	body io.Reader,
) (*transport.StreamResponse, error) {
	if session == nil {
		return nil, errors.New("anonymous graph session is nil")
	}
	header := transport.XHRHeaders(
		"application/json", "*/*",
		"https://console.cloud.google.com", "https://console.cloud.google.com/", "cross-site",
	)
	return session.DoStream(ctx, "POST", c.endpoint(apiKey), header, body)
}

func (c *Client) DoCountTokens(ctx context.Context, session *transport.Session, apiKey string, body io.Reader) (int, []byte, error) {
	if session == nil {
		return 0, nil, errors.New("anonymous graph session is nil")
	}
	header := transport.XHRHeaders(
		"application/json", "*/*", "https://console.cloud.google.com",
		"https://console.cloud.google.com/vertex-ai/studio/multimodal", "cross-site",
	)
	header["x-goog-authuser"] = []string{"0"}
	return session.DoAndRead(ctx, "POST", c.endpoint(apiKey), header, body)
}

func (c *Client) endpoint(apiKey string) string {
	c.mu.RLock()
	endpoint := c.endpointOverride
	c.mu.RUnlock()
	if endpoint == "" {
		endpoint = Endpoint(apiKey)
	}
	return endpoint
}

func Endpoint(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		apiKey = DefaultAPIKey
	}
	return BaseURL + BatchGraphQLPath + "?key=" + apiKey + "&prettyPrint=false"
}
