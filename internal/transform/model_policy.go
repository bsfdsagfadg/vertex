package transform

import (
	"fmt"
	"log"
	"strings"

	coremodel "github.com/bsfdsagfadg/vertex/internal/core/model"
)

type Diagnostic struct {
	Code    string
	Param   string
	Message string
}

type PolicyError struct {
	Code    string
	Param   string
	Message string
}

func (e *PolicyError) Error() string { return e.Message }

func ApplyModelPolicy(payload map[string]any, modelID string, policy coremodel.ParameterPolicy) ([]Diagnostic, error) {
	profile := coremodel.Resolve(modelID)
	diagnostics := make([]Diagnostic, 0)
	genCfg, _ := firstMap(payload["generationConfig"], payload["generation_config"])
	if profile.Known {
		if modalities, ok := genCfg["responseModalities"].([]any); ok {
			filtered := make([]any, 0, len(modalities))
			for _, rawModality := range modalities {
				modality := strings.ToLower(strings.TrimSpace(toString(rawModality)))
				if profile.OutputModalities[modality] {
					filtered = append(filtered, rawModality)
					continue
				}
				if policy == coremodel.PolicyPassthrough {
					filtered = append(filtered, rawModality)
					continue
				}
				if policy == coremodel.PolicyStrict {
					return nil, &PolicyError{Code: "unsupported_parameter", Param: "modalities", Message: fmt.Sprintf("model %s does not support output modality %s", modelID, modality)}
				}
				diagnostics = append(diagnostics, Diagnostic{Code: "parameter_removed", Param: "modalities", Message: "unsupported output modality removed by model profile"})
			}
			if len(filtered) == 0 && policy == coremodel.PolicyAdaptive && profile.OutputModalities["text"] {
				filtered = []any{"TEXT"}
				delete(genCfg, "audioConfig")
			}
			genCfg["responseModalities"] = filtered
		}
	}
	for field := range profile.ForbiddenGenerationFields {
		if _, present := genCfg[field]; !present {
			continue
		}
		param := geminiGenerationFieldToParam(field)
		if policy == coremodel.PolicyStrict {
			return nil, &PolicyError{Code: "unsupported_parameter", Param: param, Message: fmt.Sprintf("model %s does not support explicit parameter %s", modelID, param)}
		}
		if policy == coremodel.PolicyAdaptive {
			delete(genCfg, field)
			diagnostic := Diagnostic{Code: "parameter_removed", Param: param, Message: "removed by confirmed model profile"}
			diagnostics = append(diagnostics, diagnostic)
			log.Printf("[ProtocolPolicy] model=%s param=%s action=removed reason=confirmed_profile", modelID, param)
		}
	}
	if thinking, ok := genCfg["thinkingConfig"].(map[string]any); ok {
		if _, present := thinking["thinkingBudget"]; present {
			if policy == coremodel.PolicyStrict {
				return nil, &PolicyError{Code: "unsupported_parameter", Param: "thinking_budget", Message: fmt.Sprintf("model %s requires thinking_level instead of thinking_budget", modelID)}
			}
			if policy == coremodel.PolicyAdaptive && profile.DefaultThinkingLevel != "" {
				delete(thinking, "thinkingBudget")
				if _, present := thinking["thinkingLevel"]; !present {
					thinking["thinkingLevel"] = strings.ToUpper(profile.DefaultThinkingLevel)
				}
				diagnostics = append(diagnostics, Diagnostic{Code: "parameter_adapted", Param: "thinking_budget", Message: "converted to model thinking level"})
			}
		}
	}
	if !profile.AllowTrailingModelTurn && hasTrailingNonEmptyModelTurn(payload["contents"]) {
		return nil, &PolicyError{Code: "assistant_prefill_not_supported", Param: "messages", Message: "the final non-empty turn cannot use role model"}
	}
	if profile.RequireFunctionResponseCallID {
		if param := missingFunctionResponseIdentity(payload["contents"]); param != "" {
			return nil, &PolicyError{Code: "function_response_identity_required", Param: param, Message: "functionResponse requires both call id and name"}
		}
	}
	return diagnostics, nil
}

func hasTrailingNonEmptyModelTurn(rawContents any) bool {
	contents, _ := rawContents.([]any)
	for index := len(contents) - 1; index >= 0; index-- {
		content, ok := contents[index].(map[string]any)
		if !ok {
			continue
		}
		parts, _ := content["parts"].([]any)
		if len(parts) == 0 {
			continue
		}
		return strings.EqualFold(toString(content["role"]), "model")
	}
	return false
}

func missingFunctionResponseIdentity(rawContents any) string {
	contents, _ := rawContents.([]any)
	for contentIndex, rawContent := range contents {
		content, _ := rawContent.(map[string]any)
		parts, _ := content["parts"].([]any)
		for partIndex, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			response, _ := part["functionResponse"].(map[string]any)
			if response == nil {
				continue
			}
			if strings.TrimSpace(toString(response["id"])) == "" || strings.TrimSpace(toString(response["name"])) == "" {
				return fmt.Sprintf("contents[%d].parts[%d].functionResponse", contentIndex, partIndex)
			}
		}
	}
	return ""
}

func geminiGenerationFieldToParam(field string) string {
	switch field {
	case "topP":
		return "top_p"
	case "topK":
		return "top_k"
	case "candidateCount":
		return "candidate_count"
	default:
		return field
	}
}
