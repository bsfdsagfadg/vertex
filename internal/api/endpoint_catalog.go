package api

import (
	"net/http"
	"strings"
)

type EndpointState string

const (
	EndpointSupported           EndpointState = "supported"
	EndpointLocalResource       EndpointState = "local_resource"
	EndpointCapabilityDependent EndpointState = "capability_dependent"
	EndpointUnsupported         EndpointState = "unsupported"
	EndpointPlanned             EndpointState = "planned"
)

type EndpointSpec struct {
	Dialect string
	Method  string
	Path    string
	State   EndpointState
}

// EndpointCatalog is the runtime counterpart of docs/architecture/endpoint-manifest.yaml.
// Unsupported and not-yet-enabled official endpoints terminate with a stable
// protocol error instead of falling through to a generic HTML/404 response.
var EndpointCatalog = []EndpointSpec{ //nolint:gochecknoglobals
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/models", State: EndpointSupported},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/models/{id}", State: EndpointSupported},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/chat/completions", State: EndpointSupported},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/chat/completions", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/chat/completions/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/chat/completions/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodDelete, Path: "/v1/chat/completions/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/chat/completions/{id}/messages", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/completions", State: EndpointSupported},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/responses", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/responses/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodDelete, Path: "/v1/responses/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/responses/{id}/cancel", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/responses/{id}/input_items", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/responses/compact", State: EndpointUnsupported},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/responses/input_tokens", State: EndpointCapabilityDependent},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/conversations", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/conversations/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/conversations/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodDelete, Path: "/v1/conversations/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/conversations/{id}/items", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/conversations/{id}/items", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/conversations/{id}/items/{item_id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodDelete, Path: "/v1/conversations/{id}/items/{item_id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/images/generations", State: EndpointCapabilityDependent},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/images/edits", State: EndpointCapabilityDependent},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/images/variations", State: EndpointCapabilityDependent},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/audio/speech", State: EndpointCapabilityDependent},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/audio/transcriptions", State: EndpointCapabilityDependent},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/audio/translations", State: EndpointCapabilityDependent},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/files", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/files", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/files/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodDelete, Path: "/v1/files/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/files/{id}/content", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/batches", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/batches", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodGet, Path: "/v1/batches/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/batches/{id}/cancel", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodDelete, Path: "/v1/batches/{id}", State: EndpointLocalResource},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/embeddings", State: EndpointUnsupported},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/moderations", State: EndpointUnsupported},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/uploads", State: EndpointUnsupported},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/videos", State: EndpointUnsupported},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/vector_stores", State: EndpointUnsupported},
	{Dialect: "openai", Method: http.MethodPost, Path: "/v1/fine_tuning/jobs", State: EndpointUnsupported},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/models", State: EndpointSupported},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/models/{model}", State: EndpointSupported},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/models/{model}:generateContent", State: EndpointSupported},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/models/{model}:streamGenerateContent", State: EndpointSupported},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/models/{model}:countTokens", State: EndpointCapabilityDependent},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/models/{model}:embedContent", State: EndpointUnsupported},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/models/{model}:batchEmbedContents", State: EndpointUnsupported},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/models/{model}:asyncBatchEmbedContent", State: EndpointUnsupported},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/models/{model}:batchGenerateContent", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/interactions", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/interactions/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/interactions/{id}/cancel", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodDelete, Path: "/v1beta/interactions/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/upload/v1beta/files", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/files", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/files", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/files/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodDelete, Path: "/v1beta/files/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/cachedContents", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/cachedContents", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/cachedContents/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPatch, Path: "/v1beta/cachedContents/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodDelete, Path: "/v1beta/cachedContents/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/batches", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/batches", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodGet, Path: "/v1beta/batches/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/batches/{id}/cancel", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodDelete, Path: "/v1beta/batches/{id}", State: EndpointLocalResource},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/fileSearchStores", State: EndpointUnsupported},
	{Dialect: "gemini", Method: http.MethodPost, Path: "/v1beta/auth_tokens", State: EndpointUnsupported},
}

func lookupEndpoint(method, path string) (EndpointSpec, bool) {
	for _, endpoint := range EndpointCatalog {
		if endpoint.Method == method && endpointPathMatches(endpoint.Path, path) {
			return endpoint, true
		}
	}
	return EndpointSpec{}, false
}

func lookupEndpointPath(path string) (EndpointSpec, bool) {
	for _, endpoint := range EndpointCatalog {
		if endpointPathMatches(endpoint.Path, path) {
			return endpoint, true
		}
	}
	return EndpointSpec{}, false
}

func endpointPathMatches(pattern, path string) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for index, expected := range patternParts {
		actual := pathParts[index]
		open, close := strings.Index(expected, "{"), strings.Index(expected, "}")
		if open < 0 || close < open {
			if expected != actual {
				return false
			}
			continue
		}
		prefix, suffix := expected[:open], expected[close+1:]
		if !strings.HasPrefix(actual, prefix) || !strings.HasSuffix(actual, suffix) || len(actual) <= len(prefix)+len(suffix) {
			return false
		}
	}
	return true
}

func writeUnsupportedEndpoint(w http.ResponseWriter, endpoint EndpointSpec) {
	message := "Endpoint " + endpoint.Path + " is not implemented by this anonymous upstream proxy."
	if endpoint.Dialect == "gemini" {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": map[string]any{
			"code": http.StatusNotImplemented, "message": message, "status": "UNIMPLEMENTED", "details": []any{},
		}})
		return
	}
	writeJSON(w, http.StatusNotImplemented, map[string]any{"error": map[string]any{
		"message": message, "type": "invalid_request_error", "param": nil, "code": "unsupported_endpoint",
	}})
}
