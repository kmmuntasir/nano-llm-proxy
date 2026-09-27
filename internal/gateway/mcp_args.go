package gateway

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// This file decodes MCP tool arguments. Values stay raw until the handler asks
// for them, because LLM tool arguments routinely arrive in an encoding the
// model's own tool call did not use: clients and model hosts re-serialise tool
// arguments, and the common degradation is scalar -> string, so a model that
// meant maxResults=5 gets "5" and render=true gets "true".
//
// That matters more than it looks. Clients validate arguments against the
// advertised inputSchema *before* sending the request, so a schema that
// advertises only "integer" makes those clients reject the call outright and
// the tool never runs. The advertised schemas in mcp.go therefore admit the
// stringified form too, and the coercions here turn it back into the value
// the caller meant. Anything outside the advertised union is still rejected,
// with a message naming the argument and the encoding that was rejected.

// toolArgs is one tools/call argument object, left undecoded.
type toolArgs map[string]json.RawMessage

// decodeToolArgs parses a tools/call argument object. An absent object decodes
// to an empty set rather than an error, so "no arguments" reaches the handler
// and produces a field-specific message instead of a generic parse failure.
func decodeToolArgs(raw json.RawMessage) (toolArgs, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return toolArgs{}, nil
	}
	var args toolArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	return args, nil
}

// isAbsent reports whether an argument was omitted or sent as null, i.e. the
// caller is not expressing an opinion about it and the default should apply.
func isAbsent(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// stringArg reads a required string argument. A number or boolean is rendered
// as text rather than rejected: a model searching for a year should not fail
// because it sent query=2024.
func (a toolArgs) stringArg(name string) (string, error) {
	raw := a[name]
	if isAbsent(raw) {
		return "", fmt.Errorf("%s is required", name)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s), nil
	}
	var scalar any
	if err := json.Unmarshal(raw, &scalar); err == nil {
		switch v := scalar.(type) {
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64), nil
		case bool:
			return strconv.FormatBool(v), nil
		}
	}
	return "", fmt.Errorf("%s must be a string, got %s", name, previewJSON(raw))
}

// intArg reads an optional integer argument, accepting a JSON number, a number
// with a zero fraction (10.0), or a numeric string. Absent yields def.
func (a toolArgs) intArg(name string, def int) (int, error) {
	raw := a[name]
	if isAbsent(raw) {
		return def, nil
	}
	// json.Number accepts both 10 and "10": encoding/json validates a JSON
	// string against isValidNumber before storing it, so a non-numeric string
	// fails here rather than reaching the float parse below.
	var num json.Number
	if err := json.Unmarshal(raw, &num); err != nil {
		return 0, fmt.Errorf("%s must be an integer or a numeric string, got %s", name, previewJSON(raw))
	}
	f, err := num.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, fmt.Errorf("%s must be a whole number, got %s", name, previewJSON(raw))
	}
	if f > math.MaxInt32 || f < math.MinInt32 {
		return 0, fmt.Errorf("%s is out of range: %s", name, previewJSON(raw))
	}
	return int(f), nil
}

// boolArg reads an optional boolean argument, accepting a JSON boolean or the
// string spellings clients emit when they stringify scalars. Absent yields
// false. The string branch is explicit: json.Number only round-trips numeric
// strings, so "true" has to be matched as a string.
func (a toolArgs) boolArg(name string) (bool, error) {
	raw := a[name]
	if isAbsent(raw) {
		return false, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "1", "true", "yes":
			return true, nil
		case "0", "false", "no":
			return false, nil
		}
		return false, fmt.Errorf("%s must be a boolean or \"true\"/\"false\", got %s", name, previewJSON(raw))
	}
	return false, fmt.Errorf("%s must be a boolean or \"true\"/\"false\", got %s", name, previewJSON(raw))
}

// previewJSON renders a rejected value for an error message, bounded so a
// client that sent a megabyte of text does not flood the agent's context.
func previewJSON(raw json.RawMessage) string {
	s := string(raw)
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
