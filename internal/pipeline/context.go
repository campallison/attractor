package pipeline

import "sync"

// Context is a thread-safe key-value store shared across all stages during a
// pipeline run. Handlers read context values and return ContextUpdates in
// their Outcome; the engine merges those updates after each node completes.
type Context struct {
	mu     sync.RWMutex
	values map[string]string
	logs   []string
}

// NewContext creates an empty context.
func NewContext() *Context {
	return &Context{values: make(map[string]string)}
}

// Set writes a single key-value pair.
func (c *Context) Set(key, value string) {
	c.mu.Lock()
	c.values[key] = value
	c.mu.Unlock()
}

// Get returns the value for a key, or the fallback if unset.
func (c *Context) Get(key, fallback string) string {
	c.mu.RLock()
	v, ok := c.values[key]
	c.mu.RUnlock()
	if !ok {
		return fallback
	}
	return v
}

// GetString is an alias for Get with an empty-string fallback.
func (c *Context) GetString(key string) string {
	return c.Get(key, "")
}

// Snapshot returns a shallow copy of all values for serialization.
func (c *Context) Snapshot() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := make(map[string]string, len(c.values))
	for k, v := range c.values {
		snap[k] = v
	}
	return snap
}

// Clone returns a deep copy of the context, suitable for parallel branch
// isolation.
func (c *Context) Clone() *Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := &Context{
		values: make(map[string]string, len(c.values)),
		logs:   make([]string, len(c.logs)),
	}
	for k, v := range c.values {
		n.values[k] = v
	}
	copy(n.logs, c.logs)
	return n
}

// ApplyUpdates merges a map of key-value updates into the context.
func (c *Context) ApplyUpdates(updates map[string]string) {
	if len(updates) == 0 {
		return
	}
	c.mu.Lock()
	for k, v := range updates {
		c.values[k] = v
	}
	c.mu.Unlock()
}

// AppendLog adds an entry to the append-only run log.
func (c *Context) AppendLog(entry string) {
	c.mu.Lock()
	c.logs = append(c.logs, entry)
	c.mu.Unlock()
}

// Logs returns a copy of the run log.
func (c *Context) Logs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.logs))
	copy(out, c.logs)
	return out
}
