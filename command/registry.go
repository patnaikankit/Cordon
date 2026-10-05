package command

import (
	"sync"
)

// Registry manages allowlisted commands in the sandbox.
type Registry struct {
	mu   sync.RWMutex
	cmds map[string]Command
}

// NewRegistry creates an empty Command registry.
func NewRegistry() *Registry {
	return &Registry{
		cmds: make(map[string]Command),
	}
}

// Register adds one or more commands to the registry.
func (r *Registry) Register(cmds ...Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cmd := range cmds {
		if cmd != nil {
			r.cmds[cmd.Name()] = cmd
		}
	}
}

// Lookup finds a command by name.
func (r *Registry) Lookup(name string) (Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cmd, ok := r.cmds[name]
	return cmd, ok
}

// Names returns the names of all registered commands.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.cmds))
	for name := range r.cmds {
		names = append(names, name)
	}
	return names
}

// Len returns the count of registered commands.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.cmds)
}
