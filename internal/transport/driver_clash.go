package transport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"gopkg.in/yaml.v3"
)


// ClashDriver handles Clash protocol URIs (clash://<base64-json-or-yaml>).
type ClashDriver struct{}

func (d *ClashDriver) Scheme() string {
	return "clash"
}

func (d *ClashDriver) ParseURI(rawURI string) (map[string]any, error) {
	b64Str := rawURI
	if strings.HasPrefix(strings.ToLower(b64Str), "clash://") {
		b64Str = b64Str[8:]
	}
	if idx := strings.Index(b64Str, "#"); idx != -1 {
		b64Str = b64Str[:idx]
	}

	decoded, err := base64.StdEncoding.DecodeString(strutil.PadB64(b64Str))
	if err != nil {
		if loose, errLoose := strutil.DecodeBase64Loose(b64Str); errLoose == nil {
			decoded = loose
		} else {
			return nil, fmt.Errorf("clash base64 decode failed: %w", err)
		}
	}

	var jsonMap map[string]any
	if jsonErr := json.Unmarshal(decoded, &jsonMap); jsonErr == nil && len(jsonMap) > 0 {
		return jsonMap, nil
	}

	var rawYaml any
	if yamlErr := yaml.Unmarshal(decoded, &rawYaml); yamlErr == nil {
		m := NormalizeYAMLMap(rawYaml)
		if len(m) > 0 {
			return m, nil
		}
	}

	return nil, fmt.Errorf("clash parse failed: payload is neither valid JSON nor YAML")
}

func (d *ClashDriver) FormatURI(cfg map[string]any) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("clash marshal json error: %w", err)
	}
	return "clash://" + base64.StdEncoding.EncodeToString(data), nil
}
