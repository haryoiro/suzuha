package tool

import "sync"

// Registry is a thread-safe collection of all available tools.
type Registry struct {
	mu       sync.RWMutex
	tools    map[string]Tool
	disabled map[string]bool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:    make(map[string]Tool),
		disabled: make(map[string]bool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Unregister removes a tool from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns all registered tools.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// AllEnabled returns all registered tools that are not disabled.
func (r *Registry) AllEnabled() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if !r.disabled[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

// AllEnabledNames returns the names of all enabled tools.
func (r *Registry) AllEnabledNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for _, t := range r.tools {
		if !r.disabled[t.Name()] {
			out = append(out, t.Name())
		}
	}
	return out
}

// SetDisabled replaces the disabled set with the given tool names.
func (r *Registry) SetDisabled(names []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disabled = make(map[string]bool, len(names))
	for _, n := range names {
		r.disabled[n] = true
	}
}

// IsDisabled returns true if the tool is disabled.
func (r *Registry) IsDisabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.disabled[name]
}

// DisabledNames returns the list of disabled tool names.
func (r *Registry) DisabledNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.disabled))
	for n := range r.disabled {
		out = append(out, n)
	}
	return out
}
