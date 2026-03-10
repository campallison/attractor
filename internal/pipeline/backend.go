package pipeline

import (
	"context"
	"errors"

	"github.com/campallison/attractor/internal/agent"
	"github.com/campallison/attractor/internal/dot"
)

// AgentBackend implements CodergenBackend by delegating to the Layer 2 agent
// loop (agent.RunTaskCapture). Each codergen node invocation runs a full
// agentic loop with tool execution.
type AgentBackend struct {
	Client        agent.Completer
	Model         string
	ModelOverride string // when non-empty, used for ALL stages regardless of node/default model
	WorkDir       string
}

func (b AgentBackend) Run(node *dot.Node, prompt string, _ *Context) (BackendResult, error) {
	model := b.ModelOverride
	if model == "" {
		model = node.Model()
		if model == "" {
			model = b.Model
		}
	}
	text, usage, rounds, conversation, err := agent.RunTaskCapture(context.Background(), b.Client, model, prompt, b.WorkDir, node.MaxRounds())
	if errors.Is(err, agent.ErrRoundLimitReached) {
		return BackendResult{
			Response:     text,
			Usage:        usage,
			Model:        model,
			Rounds:       rounds,
			Conversation: conversation,
			Exhausted:    true,
		}, nil
	}
	if err != nil {
		return BackendResult{}, err
	}
	return BackendResult{
		Response:     text,
		Usage:        usage,
		Model:        model,
		Rounds:       rounds,
		Conversation: conversation,
	}, nil
}
