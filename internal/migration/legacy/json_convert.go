package legacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

func ConvertConfig(path string) ([]byte, error) {
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read legacy config: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse legacy config: %w", err)
		}
	}
	var legacyProxy string
	if value := raw["proxy_url"]; len(value) > 0 {
		_ = json.Unmarshal(value, &legacyProxy)
	}
	delete(raw, "telemetry_enabled")
	delete(raw, "instance_id")
	delete(raw, "proxy_url")
	setDefaultJSON(raw, "global_proxy_enabled", strings.TrimSpace(legacyProxy) != "")
	setDefaultJSON(raw, "global_proxy_required", true)
	setDefaultJSON(raw, "global_proxy_selection", "health")
	setDefaultJSON(raw, "allow_direct_without_global_proxy", false)
	setDefaultJSON(raw, "openai_parameter_policy", "adaptive")
	setDefaultJSON(raw, "gemini_parameter_policy", "passthrough")
	setDefaultJSON(raw, "tool_schema_policy", "adaptive")
	return marshalStableObject(raw)
}

func ConvertModels(path string) ([]byte, error) {
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read legacy models: %w", err)
		}
		raw["models"] = json.RawMessage("[]")
		raw["alias_map"] = json.RawMessage("{}")
	} else {
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 {
			return nil, fmt.Errorf("legacy models file is empty")
		}
		if trimmed[0] == '[' {
			raw["models"] = append(json.RawMessage(nil), trimmed...)
			raw["alias_map"] = json.RawMessage("{}")
		} else if err := json.Unmarshal(trimmed, &raw); err != nil {
			return nil, fmt.Errorf("parse legacy models: %w", err)
		}
	}
	models, err := convertModelEntries(raw["models"])
	if err != nil {
		return nil, err
	}
	raw["version"] = json.RawMessage("2")
	raw["models"] = models
	if len(bytes.TrimSpace(raw["alias_map"])) == 0 || bytes.Equal(bytes.TrimSpace(raw["alias_map"]), []byte("null")) {
		raw["alias_map"] = json.RawMessage("{}")
	}
	return marshalModelsObject(raw)
}

func convertModelEntries(value json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return json.RawMessage("[]"), nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(value, &entries); err != nil {
		return nil, fmt.Errorf("parse legacy model list: %w", err)
	}
	converted := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		var id string
		if json.Unmarshal(entry, &id) == nil {
			object, err := json.Marshal(map[string]any{
				"id": id, "enabled": true, "fake_stream_enabled": true, "trailing_fix_enabled": false,
			})
			if err != nil {
				return nil, err
			}
			converted = append(converted, object)
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(entry, &object); err != nil {
			return nil, fmt.Errorf("parse legacy model entry: %w", err)
		}
		if len(bytes.TrimSpace(object["id"])) == 0 {
			return nil, fmt.Errorf("legacy model entry is missing id")
		}
		setDefaultJSON(object, "enabled", true)
		setDefaultJSON(object, "fake_stream_enabled", true)
		setDefaultJSON(object, "trailing_fix_enabled", false)
		encoded, err := marshalStableObject(object)
		if err != nil {
			return nil, err
		}
		converted = append(converted, bytes.TrimSpace(encoded))
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func setDefaultJSON(target map[string]json.RawMessage, key string, value any) {
	if _, ok := target[key]; ok {
		return
	}
	encoded, err := json.Marshal(value)
	if err == nil {
		target[key] = encoded
	}
}

func marshalStableObject(raw map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var compact bytes.Buffer
	compact.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			compact.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(key)
		compact.Write(keyJSON)
		compact.WriteByte(':')
		compact.Write(bytes.TrimSpace(raw[key]))
	}
	compact.WriteByte('}')
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}

func marshalModelsObject(raw map[string]json.RawMessage) ([]byte, error) {
	ordered := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		ordered[key] = value
	}
	keys := []string{"version", "models", "alias_map"}
	extra := make([]string, 0, len(raw))
	for key := range raw {
		if key != "version" && key != "models" && key != "alias_map" {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	keys = append(keys, extra...)
	var compact bytes.Buffer
	compact.WriteByte('{')
	for index, key := range keys {
		value, ok := ordered[key]
		if !ok {
			continue
		}
		if index > 0 {
			compact.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(key)
		compact.Write(keyJSON)
		compact.WriteByte(':')
		compact.Write(bytes.TrimSpace(value))
	}
	compact.WriteByte('}')
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}
