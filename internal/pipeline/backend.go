package pipeline

import (
	"context"

	"github.com/campallison/attractor/internal/agent"
	"github.com/campallison/attractor/internal/dot"
)

// AgentBackend implements CodergenBackend by delegating to the Layer 2 agent
// loop (agent.RunTaskCapture). Each codergen node invocation runs a full
// agentic loop with tool execution.
type AgentBackend struct {
	Client  agent.Completer
	Model   string
	WorkDir string
}

func (b AgentBackend) Run(node *dot.Node, prompt string, _ *Context) (string, error) {
	model := node.Model()
	if model == "" {
		model = b.Model
	}
	return agent.RunTaskCapture(context.Background(), b.Client, model, prompt, b.WorkDir)
}
