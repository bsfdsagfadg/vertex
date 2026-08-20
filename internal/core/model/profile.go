package model

import "strings"

type ParameterPolicy string

const (
	PolicyStrict      ParameterPolicy = "strict"
	PolicyAdaptive    ParameterPolicy = "adaptive"
	PolicyPassthrough ParameterPolicy = "passthrough"
)

type Profile struct {
	ID                            string
	Known                         bool
	GenerateContent               bool
	Interactions                  bool
	InputModalities               map[string]bool
	OutputModalities              map[string]bool
	FunctionTools                 bool
	ParallelFunctionTools         bool
	ThinkingLevels                map[string]bool
	DefaultThinkingLevel          string
	ForbiddenGenerationFields     map[string]bool
	ConditionalForbiddenFields    map[string]bool
	AllowTrailingModelTurn        bool
	RequireFunctionResponseCallID bool
	PreserveThoughtSignatures     bool
}

var conservativeProfile = Profile{ //nolint:gochecknoglobals
	GenerateContent: true,
	InputModalities: map[string]bool{"text": true}, OutputModalities: map[string]bool{"text": true},
	FunctionTools: true, ParallelFunctionTools: false,
	ThinkingLevels: map[string]bool{}, ForbiddenGenerationFields: map[string]bool{},
	ConditionalForbiddenFields: map[string]bool{}, AllowTrailingModelTurn: false,
	RequireFunctionResponseCallID: true, PreserveThoughtSignatures: true,
}

var builtInProfiles = map[string]Profile{ //nolint:gochecknoglobals
	"gemini-3.6-flash": {
		ID: "gemini-3.6-flash", Known: true, GenerateContent: true, Interactions: true,
		InputModalities:  map[string]bool{"text": true, "image": true, "video": true, "audio": true, "pdf": true},
		OutputModalities: map[string]bool{"text": true}, FunctionTools: true, ParallelFunctionTools: true,
		ThinkingLevels: map[string]bool{"medium": true, "high": true}, DefaultThinkingLevel: "medium",
		ForbiddenGenerationFields: map[string]bool{
			"temperature": true, "topP": true, "topK": true, "candidateCount": true,
		},
		ConditionalForbiddenFields: map[string]bool{}, AllowTrailingModelTurn: false,
		RequireFunctionResponseCallID: true, PreserveThoughtSignatures: true,
	},
}

func Resolve(id string) Profile {
	id = strings.TrimPrefix(strings.TrimSpace(id), "models/")
	if profile, ok := builtInProfiles[id]; ok {
		return clone(profile)
	}
	profile := clone(conservativeProfile)
	profile.ID = id
	return profile
}

func ParsePolicy(value string, fallback ParameterPolicy) ParameterPolicy {
	switch ParameterPolicy(strings.ToLower(strings.TrimSpace(value))) {
	case PolicyStrict:
		return PolicyStrict
	case PolicyAdaptive:
		return PolicyAdaptive
	case PolicyPassthrough:
		return PolicyPassthrough
	default:
		return fallback
	}
}

func clone(profile Profile) Profile {
	profile.InputModalities = cloneSet(profile.InputModalities)
	profile.OutputModalities = cloneSet(profile.OutputModalities)
	profile.ThinkingLevels = cloneSet(profile.ThinkingLevels)
	profile.ForbiddenGenerationFields = cloneSet(profile.ForbiddenGenerationFields)
	profile.ConditionalForbiddenFields = cloneSet(profile.ConditionalForbiddenFields)
	return profile
}

func cloneSet(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
