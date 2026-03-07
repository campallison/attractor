// Package tools provides the tool implementations for the Attractor coding agent.
// Layer 2: read_file, write_file, edit_file, shell, and tool registry.
package tools

import (
	"encoding/json"

	"github.com/campallison/attractor/internal/llm"
)

// ToolExecutor is the function signature for a tool's execute handler.
// It receives the raw JSON arguments and a working directory, and returns
// the tool's text output or an error.
type ToolExecutor func(args json.RawMessage, workDir string) (string, error)

// RegisteredTool pairs an LLM tool definition with its execute handler.
type RegisteredTool struct {
	Definition llm.ToolDefinition
	Execute    ToolExecutor
}

// Registry holds the set of tools available to the agent.
type Registry struct {
	tools map[string]RegisteredTool
}

// NewRegistry returns an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]RegisteredTool)}
}

// Register adds or replaces a tool in the registry.
func (r *Registry) Register(tool RegisteredTool) {
	r.tools[tool.Definition.Name] = tool
}

// Get retrieves a tool by name. Returns false if the tool is not registered.
func (r *Registry) Get(name string) (RegisteredTool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns the LLM tool definitions for all registered tools.
func (r *Registry) Definitions() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition)
	}
	return defs
}

// DefaultRegistry returns a registry pre-loaded with the standard tool set:
// read_file, write_file, edit_file, and shell.
// The dockerImage parameter specifies the Docker image/container used for
// shell command execution.
func DefaultRegistry(dockerImage string) *Registry {
	r := NewRegistry()
	r.Register(ReadFileTool())
	r.Register(WriteFileTool())
	r.Register(EditFileTool())
	r.Register(ShellTool(dockerImage))
	return r
}
