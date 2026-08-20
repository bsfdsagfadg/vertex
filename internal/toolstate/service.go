package toolstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/repository"
)

const defaultTTL = 24 * time.Hour

type Store interface {
	PutToolStates(context.Context, []repository.ToolState) error
	GetToolStates(context.Context, []string, time.Time) (map[string]repository.ToolState, error)
	ConsumeToolStates(context.Context, []string, string, time.Time) error
	DeleteExpiredToolStates(context.Context, time.Time) (int64, error)
}

type Service struct {
	store Store
	ttl   time.Duration
	now   func() time.Time
}

type ProtocolError struct {
	Code    string
	Param   string
	Message string
}

func (e *ProtocolError) Error() string { return e.Message }

type opaqueStep struct {
	Version int            `json:"version"`
	Content map[string]any `json:"content"`
	Calls   []opaqueCall   `json:"calls"`
}

type opaqueCall struct {
	ExternalCallID string `json:"external_call_id"`
	Name           string `json:"name"`
	PartIndex      int    `json:"part_index"`
}

func New(store Store) *Service {
	if store == nil {
		return nil
	}
	return &Service{store: store, ttl: defaultTTL, now: time.Now}
}

// CaptureResponse persists each candidate's complete model tool step. Every
// call in one parallel step points to the same encrypted opaque transcript.
func (s *Service) CaptureResponse(ctx context.Context, response map[string]any, responseID, conversationID, operation string) error {
	if s == nil || s.store == nil {
		return nil
	}
	states := make([]repository.ToolState, 0)
	seen := map[string]struct{}{}
	for _, candidate := range mapSlice(response["candidates"]) {
		content, _ := candidate["content"].(map[string]any)
		parts := anySlice(content["parts"])
		calls := make([]opaqueCall, 0)
		for partIndex, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			call, _ := part["functionCall"].(map[string]any)
			if call == nil {
				continue
			}
			callID := firstString(call["id"], call["call_id"], call["toolCallId"])
			name := strings.TrimSpace(stringValue(call["name"]))
			if callID == "" || name == "" {
				return &ProtocolError{Code: "tool_call_identity_missing", Param: "candidates.content.parts.functionCall", Message: "upstream tool call is missing a stable id or name"}
			}
			if _, duplicate := seen[callID]; duplicate {
				return &ProtocolError{Code: "duplicate_tool_call_id", Param: "candidates.content.parts.functionCall.id", Message: "upstream returned a duplicate tool call id"}
			}
			seen[callID] = struct{}{}
			calls = append(calls, opaqueCall{ExternalCallID: callID, Name: name, PartIndex: partIndex})
		}
		if len(calls) == 0 {
			continue
		}
		step := opaqueStep{Version: 1, Content: cloneMap(content), Calls: calls}
		stateJSON, err := json.Marshal(step)
		if err != nil {
			return fmt.Errorf("encode opaque tool step: %w", err)
		}
		transcriptHash := sha256Hex(stateJSON)
		expiresAt := s.now().UTC().Add(s.ttl).Unix()
		for _, call := range calls {
			states = append(states, repository.ToolState{
				ExternalCallID: call.ExternalCallID, ResponseID: responseID,
				ConversationID: conversationID, UpstreamOperation: operation,
				StateJSON: stateJSON, ExpiresAt: expiresAt, TranscriptHash: transcriptHash,
			})
		}
	}
	return s.store.PutToolStates(ctx, states)
}

// RestoreOpenAIChat replaces client-replayed assistant tool calls with the
// exact upstream model step and validates the following parallel results.
func (s *Service) RestoreOpenAIChat(ctx context.Context, payload map[string]any) error {
	if s == nil || s.store == nil {
		return &ProtocolError{Code: "tool_state_unavailable", Param: "messages", Message: "tool state storage is unavailable"}
	}
	contents := anySlice(payload["contents"])
	for contentIndex := 0; contentIndex < len(contents); contentIndex++ {
		content, _ := contents[contentIndex].(map[string]any)
		calls, err := callsFromContent(content)
		if err != nil {
			return err
		}
		if len(calls) == 0 {
			if hasFunctionResponses(content) {
				return &ProtocolError{Code: "tool_state_missing", Param: fmt.Sprintf("messages[%d]", contentIndex), Message: "tool result has no preceding tool call state"}
			}
			continue
		}

		resultEnd := contentIndex + 1
		results := make([]functionResult, 0, len(calls))
		for resultEnd < len(contents) {
			resultContent, _ := contents[resultEnd].(map[string]any)
			if !hasFunctionResponses(resultContent) {
				break
			}
			parsed, parseErr := resultsFromContent(resultContent, resultEnd)
			if parseErr != nil {
				return parseErr
			}
			results = append(results, parsed...)
			resultEnd++
		}
		if len(results) == 0 {
			return &ProtocolError{Code: "incomplete_tool_transcript", Param: fmt.Sprintf("messages[%d]", contentIndex), Message: "assistant tool calls must be followed by their tool results"}
		}

		callIDs := make([]string, 0, len(calls))
		for _, call := range calls {
			callIDs = append(callIDs, call.ExternalCallID)
		}
		stored, err := s.store.GetToolStates(ctx, callIDs, s.now())
		if err != nil {
			return fmt.Errorf("restore tool state: %w", err)
		}
		for _, callID := range callIDs {
			if _, ok := stored[callID]; !ok {
				return &ProtocolError{Code: "tool_state_missing", Param: "messages.tool_call_id", Message: "tool call state is missing or expired"}
			}
		}
		step, transcriptHash, err := decodeConsistentStep(stored, callIDs)
		if err != nil {
			return err
		}
		if err := validateCallSet(calls, step.Calls); err != nil {
			return err
		}
		if err := applyAndValidateResults(contents[contentIndex+1:resultEnd], results, step.Calls); err != nil {
			return err
		}
		contents[contentIndex] = cloneMap(step.Content)

		consumeJSON, err := json.Marshal(results)
		if err != nil {
			return fmt.Errorf("encode tool results: %w", err)
		}
		consumeHash := sha256Hex(append([]byte(transcriptHash+"\x00"), consumeJSON...))
		if err := s.store.ConsumeToolStates(ctx, callIDs, consumeHash, s.now()); err != nil {
			if errors.Is(err, repository.ErrToolStateConsumed) {
				return &ProtocolError{Code: "tool_state_consumed", Param: "messages.tool_call_id", Message: "tool call state was already consumed by different results"}
			}
			return fmt.Errorf("consume tool state: %w", err)
		}
		contentIndex = resultEnd - 1
	}
	payload["contents"] = contents
	return nil
}

func callsFromContent(content map[string]any) ([]opaqueCall, error) {
	calls := make([]opaqueCall, 0)
	seen := map[string]struct{}{}
	for partIndex, rawPart := range anySlice(content["parts"]) {
		part, _ := rawPart.(map[string]any)
		call, _ := part["functionCall"].(map[string]any)
		if call == nil {
			continue
		}
		callID := firstString(call["id"], call["call_id"], call["toolCallId"])
		name := strings.TrimSpace(stringValue(call["name"]))
		if callID == "" || name == "" {
			return nil, &ProtocolError{Code: "tool_call_identity_missing", Param: "messages.tool_calls", Message: "tool calls require both id and function name"}
		}
		if _, ok := seen[callID]; ok {
			return nil, &ProtocolError{Code: "duplicate_tool_call_id", Param: "messages.tool_calls.id", Message: "duplicate tool call id"}
		}
		seen[callID] = struct{}{}
		calls = append(calls, opaqueCall{ExternalCallID: callID, Name: name, PartIndex: partIndex})
	}
	return calls, nil
}

type functionResult struct {
	ExternalCallID string         `json:"external_call_id"`
	Name           string         `json:"name"`
	Response       map[string]any `json:"response"`
	ContentIndex   int            `json:"-"`
	PartIndex      int            `json:"-"`
}

func resultsFromContent(content map[string]any, contentIndex int) ([]functionResult, error) {
	results := make([]functionResult, 0)
	for partIndex, rawPart := range anySlice(content["parts"]) {
		part, _ := rawPart.(map[string]any)
		response, _ := part["functionResponse"].(map[string]any)
		if response == nil {
			continue
		}
		callID := firstString(response["id"], response["call_id"], response["toolCallId"])
		if callID == "" {
			return nil, &ProtocolError{Code: "tool_call_id_required", Param: fmt.Sprintf("messages[%d]", contentIndex), Message: "tool result requires tool_call_id"}
		}
		body, _ := response["response"].(map[string]any)
		results = append(results, functionResult{ExternalCallID: callID, Name: strings.TrimSpace(stringValue(response["name"])), Response: cloneMap(body), ContentIndex: contentIndex, PartIndex: partIndex})
	}
	return results, nil
}

func applyAndValidateResults(contents []any, results []functionResult, storedCalls []opaqueCall) error {
	callByID := make(map[string]opaqueCall, len(storedCalls))
	for _, call := range storedCalls {
		callByID[call.ExternalCallID] = call
	}
	seen := map[string]struct{}{}
	for _, result := range results {
		call, ok := callByID[result.ExternalCallID]
		if !ok {
			return &ProtocolError{Code: "tool_call_id_mismatch", Param: "messages.tool_call_id", Message: "tool result references a call outside the preceding parallel step"}
		}
		if _, duplicate := seen[result.ExternalCallID]; duplicate {
			return &ProtocolError{Code: "duplicate_tool_result", Param: "messages.tool_call_id", Message: "a tool call has more than one result"}
		}
		seen[result.ExternalCallID] = struct{}{}
		if result.Name != "" && result.Name != call.Name {
			return &ProtocolError{Code: "tool_name_mismatch", Param: "messages.name", Message: "tool result name does not match tool_call_id"}
		}
		for _, rawContent := range contents {
			content, _ := rawContent.(map[string]any)
			for _, rawPart := range anySlice(content["parts"]) {
				part, _ := rawPart.(map[string]any)
				response, _ := part["functionResponse"].(map[string]any)
				if response != nil && firstString(response["id"], response["call_id"], response["toolCallId"]) == result.ExternalCallID {
					response["name"] = call.Name
				}
			}
		}
	}
	if len(seen) != len(callByID) {
		return &ProtocolError{Code: "incomplete_tool_transcript", Param: "messages", Message: "all calls in a parallel tool step must have exactly one result"}
	}
	return nil
}

func decodeConsistentStep(states map[string]repository.ToolState, callIDs []string) (opaqueStep, string, error) {
	var step opaqueStep
	var transcriptHash string
	for _, callID := range callIDs {
		state := states[callID]
		if sha256Hex(state.StateJSON) != state.TranscriptHash {
			return opaqueStep{}, "", &ProtocolError{Code: "tool_state_invalid", Param: "messages.tool_call_id", Message: "stored tool state failed transcript integrity validation"}
		}
		if transcriptHash == "" {
			transcriptHash = state.TranscriptHash
			if err := json.Unmarshal(state.StateJSON, &step); err != nil {
				return opaqueStep{}, "", fmt.Errorf("decode opaque tool state: %w", err)
			}
			continue
		}
		if state.TranscriptHash != transcriptHash {
			return opaqueStep{}, "", &ProtocolError{Code: "tool_state_mismatch", Param: "messages.tool_call_id", Message: "parallel tool calls do not belong to the same upstream step"}
		}
	}
	if step.Version != 1 || step.Content == nil || len(step.Calls) == 0 {
		return opaqueStep{}, "", &ProtocolError{Code: "tool_state_invalid", Param: "messages.tool_call_id", Message: "stored tool state is invalid"}
	}
	return step, transcriptHash, nil
}

func validateCallSet(client, stored []opaqueCall) error {
	clientPairs := make([]string, 0, len(client))
	storedPairs := make([]string, 0, len(stored))
	for _, call := range client {
		clientPairs = append(clientPairs, call.ExternalCallID+"\x00"+call.Name)
	}
	for _, call := range stored {
		storedPairs = append(storedPairs, call.ExternalCallID+"\x00"+call.Name)
	}
	sort.Strings(clientPairs)
	sort.Strings(storedPairs)
	if strings.Join(clientPairs, "\x01") != strings.Join(storedPairs, "\x01") {
		return &ProtocolError{Code: "tool_state_mismatch", Param: "messages.tool_calls", Message: "tool calls do not match the stored upstream step"}
	}
	return nil
}

func hasFunctionResponses(content map[string]any) bool {
	for _, rawPart := range anySlice(content["parts"]) {
		part, _ := rawPart.(map[string]any)
		if _, ok := part["functionResponse"].(map[string]any); ok {
			return true
		}
	}
	return false
}

func mapSlice(raw any) []map[string]any {
	items := anySlice(raw)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if value, ok := item.(map[string]any); ok {
			result = append(result, value)
		}
	}
	return result
}

func anySlice(raw any) []any {
	items, _ := raw.([]any)
	return items
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringValue(value)); text != "" {
			return text
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	var cloned map[string]any
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
