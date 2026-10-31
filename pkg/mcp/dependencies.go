package mcp

import "github.com/anra-studio/anra/pkg/model/projection"

// toolDependencies owns the open calls in one committed window. A response
// chunk keeps its call open while willContinue is true. Only the terminal
// response (false or absent) closes it. Neither an anchor nor the window end
// may be crossed with an open dependency.
type toolDependencies map[string]string

func toolKey(id, name string) string {
	if id != "" {
		return "id:" + id
	}
	return "name:" + name
}

func (d toolDependencies) observe(part *projection.Part) error {
	// The shared projection already validates required tool names.
	if call := part.FunctionCall; call != nil {
		key := toolKey(call.ID, call.Name)
		if d[key] != "" {
			return ErrFailedPrecondition
		}
		d[key] = call.Name
	}
	if response := part.FunctionResponse; response != nil {
		key := toolKey(response.ID, response.Name)
		if d[key] != response.Name {
			return ErrFailedPrecondition
		}
		if response.WillContinue == nil || !*response.WillContinue {
			delete(d, key)
		}
	}
	return nil
}
