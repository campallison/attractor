package pipeline

import (
	"context"

	"github.com/campallison/attractor/internal/dot"
)

// Handler is the interface that every node handler must implement. The
// execution engine dispatches to the appropriate handler based on the node's
// type or shape.
type Handler interface {
	Execute(ctx context.Context, node *dot.Node, pctx *Context, g *dot.Graph, logsRoot string) Outcome
}

// HandlerRegistry maps handler type strings to handler instances and provides
// resolution logic following the spec's precedence: explicit type -> shape ->
// default.
type HandlerRegistry struct {
	handlers       map[string]Handler
	defaultHandler Handler
}

// NewHandlerRegistry creates an empty registry with the given default handler.
func NewHandlerRegistry(defaultHandler Handler) *HandlerRegistry {
	return &HandlerRegistry{
		handlers:       make(map[string]Handler),
		defaultHandler: defaultHandler,
	}
}

// Register adds or replaces a handler for the given type string.
func (r *HandlerRegistry) Register(typeName string, h Handler) {
	r.handlers[typeName] = h
}

// ShapeToHandlerType maps Graphviz shapes to their canonical handler types.
var ShapeToHandlerType = map[string]string{
	"Mdiamond":      "start",
	"Msquare":       "exit",
	"box":           "codergen",
	"hexagon":       "wait.human",
	"diamond":       "conditional",
	"component":     "parallel",
	"tripleoctagon": "parallel.fan_in",
	"parallelogram": "tool",
	"house":         "stack.manager_loop",
}

// Resolve returns the handler for a node using the spec's resolution order:
//  1. Explicit type attribute
//  2. Shape-based resolution
//  3. Default handler
func (r *HandlerRegistry) Resolve(node *dot.Node) Handler {
	if typ := node.Type(); typ != "" {
		if h, ok := r.handlers[typ]; ok {
			return h
		}
	}
	if handlerType, ok := ShapeToHandlerType[node.Shape()]; ok {
		if h, ok := r.handlers[handlerType]; ok {
			return h
		}
	}
	return r.defaultHandler
}
