package pipeline

import (
	"context"
	"errors"

	"github.com/campallison/attractor/internal/agent"
	"github.com/campallison/attractor/internal/dot"
	"github.com/campallison/attractor/internal/tools"
)

// AgentBackend implements CodergenBackend by delegating to the Layer 2 agent
// loop (agent.RunTaskCapture). Each codergen node invocation runs a full
// agentic loop with tool execution.
type AgentBackend struct {
	Client        agent.Completer
	Model         string
	ModelOverride string // when non-empty, used for ALL stages regardless of node/default model
	WorkDir       string
	Registry      *tools.Registry
}

func (b AgentBackend) Run(ctx context.Context, node *dot.Node, prompt string, _ *Context) (BackendResult, error) {
	model := b.ModelOverride
	if model == "" {
		model = node.Model()
		if model == "" {
			model = b.Model
		}
	}
	text, usage, rounds, conversation, err := agent.RunTaskCapture(ctx, b.Client, model, prompt, b.WorkDir, node.MaxRounds(), b.Registry)
	if errors.Is(err, agent.ErrRoundLimitReached) || errors.Is(err, agent.ErrReadLoopDetected) {
		reason := ExhaustionRoundLimit
		if errors.Is(err, agent.ErrReadLoopDetected) {
			reason = ExhaustionReadLoop
		}
		return BackendResult{
			Response:         text,
			Usage:            usage,
			Model:            model,
			Rounds:           rounds,
			Conversation:     conversation,
			Exhausted:        true,
			ExhaustionReason: reason,
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
