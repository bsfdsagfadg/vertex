package vertex

import "github.com/bsfdsagfadg/vertex/internal/infra/transport"

func (c *VertexAIClient) Net() *transport.NetworkClient { return c.net }
