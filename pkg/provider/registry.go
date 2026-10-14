package provider

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Config is the per-provider runtime configuration assembled by the host
// application (typically from environment variables or a config file). Keys are
// provider specific, e.g. {"api_key": "...", "base_url": "..."}.
type Config map[string]string

// Get returns the value for key, or def when absent.
func (c Config) Get(key, def string) string {
	if v, ok := c[key]; ok && v != "" {
		return v
	}
	return def
}

// Required returns the value for key or an error when it is missing.
func (c Config) Required(key string) (string, error) {
	if v, ok := c[key]; ok && strings.TrimSpace(v) != "" {
		return v, nil
	}
	return "", fmt.Errorf("provider: missing required config %q", key)
}

// Factory builds a ready-to-use [Provider] from its configuration. Providers
// register a Factory so the host application can construct them lazily and
// generically from configuration.
type Factory func(cfg Config) (Provider, error)

// Registry holds provider factories and their built instances.
//
// Factories are typically registered from provider package init functions:
//
//	func init() {
//		provider.Register("midtrans", func(cfg provider.Config) (provider.Provider, error) {
//			return midtrans.New(cfg), nil
//		})
//	}
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	instances map[string]Provider
}

// NewRegistry returns a registry pre-populated with every factory registered in
// the default registry, mirroring how database/sql inherits registered drivers.
// This means `provider.Register` in a provider's init() is enough for the host
// application to build that provider from configuration.
func NewRegistry() *Registry {
	return &Registry{
		factories: defaultRegistry.snapshotFactories(),
		instances: make(map[string]Provider),
	}
}

// snapshotFactories copies the registered factories under a read lock.
func (r *Registry) snapshotFactories() map[string]Factory {
	if r == nil {
		return map[string]Factory{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Factory, len(r.factories))
	for name, factory := range r.factories {
		out[name] = factory
	}
	return out
}

// RegisterFactory associates a name with a Factory. It panics on invalid input
// because registration happens at startup, where failing fast is desirable.
func (r *Registry) RegisterFactory(name string, f Factory) {
	name = normalizeName(name)
	if name == "" {
		panic("provider: RegisterFactory called with empty name")
	}
	if f == nil {
		panic("provider: RegisterFactory called with nil factory for " + name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[name]; dup {
		panic("provider: duplicate factory registration for " + name)
	}
	r.factories[name] = f
}

// Add stores an already constructed provider instance under its own name.
func (r *Registry) Add(p Provider) {
	if p == nil {
		panic("provider: Add called with nil provider")
	}
	name := normalizeName(p.Name())
	if name == "" {
		panic("provider: Add called with provider having empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[name] = p
}

// Build instantiates the factory registered under name with cfg and stores the
// result. It returns an error when no factory is registered.
func (r *Registry) Build(name string, cfg Config) (Provider, error) {
	name = normalizeName(name)
	r.mu.RLock()
	f, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider: no factory registered for %q", name)
	}
	p, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("provider: building %q: %w", name, err)
	}
	if p == nil {
		return nil, fmt.Errorf("provider: factory for %q returned nil", name)
	}
	r.Add(p)
	return p, nil
}

// Get returns the built instance for name.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.instances[normalizeName(name)]
	return p, ok
}

// MustGet returns the built instance for name or panics.
func (r *Registry) MustGet(name string) Provider {
	p, ok := r.Get(name)
	if !ok {
		panic("provider: not registered: " + name)
	}
	return p
}

// Names returns the sorted names of all built instances.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.instances))
	for n := range r.instances {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// FactoryNames returns the sorted names of all registered factories.
func (r *Registry) FactoryNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Len returns the number of built instances.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.instances)
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// defaultRegistry backs the package-level helpers below. It is initialized as a
// literal (not via NewRegistry) to avoid an initialization cycle.
var defaultRegistry = &Registry{
	factories: make(map[string]Factory),
	instances: make(map[string]Provider),
}

// Default returns the process-wide provider registry.
func Default() *Registry { return defaultRegistry }

// Register registers a Factory in the default registry.
func Register(name string, f Factory) { defaultRegistry.RegisterFactory(name, f) }

// RegisterProvider stores a pre-built Provider in the default registry.
func RegisterProvider(p Provider) { defaultRegistry.Add(p) }
