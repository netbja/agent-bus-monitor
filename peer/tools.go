package peer

// ToolSpec is one OpenAI function tool.
type ToolSpec struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is a tool's name, description and JSON-schema parameters.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func tool(name, desc string, props map[string]any, required ...string) ToolSpec {
	params := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		params["required"] = required
	}
	return ToolSpec{Type: "function", Function: ToolFunction{Name: name, Description: desc, Parameters: params}}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

// ToolSpecs are the peer's tools, all read-only: no shell, no write, no URL.
func ToolSpecs() []ToolSpec {
	return []ToolSpec{
		tool("read_file", "Read a text file under the project root (64 KiB at most).",
			map[string]any{"path": str("Path relative to the project root"), "offset": num("First byte to read"), "limit": num("Bytes to read")}, "path"),
		tool("list_dir", "List the names in a directory under the project root.",
			map[string]any{"path": str("Directory relative to the project root; empty for the root")}),
		tool("grep", "Search files under the project root for a Go regular expression.",
			map[string]any{"pattern": str("Go regexp"), "path": str("File or directory relative to the root; empty for the root")}, "pattern"),
		tool("bus_thread", "Read a bus thread's entries (the last 20).",
			map[string]any{"id": str("Thread id: a message id or a ref")}, "id"),
		tool("bus_agents", "List the agents on the bus and their states.", map[string]any{}),
	}
}
