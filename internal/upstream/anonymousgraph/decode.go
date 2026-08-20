package anonymousgraph

// Event is one anonymous Graph protocol event before downstream protocol
// projection. Exactly one of Payload or Errors is normally populated.
type Event struct {
	Payload map[string]any
	Errors  []any
}

// DecodeObject owns the anonymous Graph results/data/ui wrapper. It deliberately
// does not interpret Gemini candidates or construct downstream error envelopes.
func DecodeObject(object map[string]any) []Event {
	results, _ := object["results"].([]any)
	events := make([]Event, 0, len(results))
	for _, rawResult := range results {
		result, ok := rawResult.(map[string]any)
		if !ok {
			continue
		}
		if graphErrors, ok := result["errors"].([]any); ok && len(graphErrors) > 0 {
			events = append(events, Event{Errors: graphErrors})
			continue
		}
		data, ok := result["data"].(map[string]any)
		if !ok {
			continue
		}
		inner := any(data)
		if ui, ok := data["ui"].(map[string]any); ok {
			if value, exists := ui["streamGenerateContentAnonymous"]; exists {
				inner = value
			}
		}
		switch value := inner.(type) {
		case map[string]any:
			events = append(events, Event{Payload: value})
		case []any:
			metadata := outerMetadata(data)
			for _, rawItem := range value {
				item, ok := rawItem.(map[string]any)
				if !ok {
					continue
				}
				cloned := make(map[string]any, len(item)+len(metadata))
				for key, field := range item {
					cloned[key] = field
				}
				for key, field := range metadata {
					if _, exists := cloned[key]; !exists {
						cloned[key] = field
					}
				}
				events = append(events, Event{Payload: cloned})
			}
		}
	}
	return events
}

func outerMetadata(data map[string]any) map[string]any {
	metadata := make(map[string]any, 4)
	for _, key := range []string{"usageMetadata", "modelVersion", "responseId", "promptFeedback"} {
		if value, ok := data[key]; ok && value != nil {
			metadata[key] = value
		}
	}
	return metadata
}
