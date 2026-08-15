package marketmaker

import (
	"fmt"
	"sort"
)

// Adding a new exchange = implement FeedSource or ExecExchange and Register* it in
// an init(). Config then selects it by name; no other code changes.
type (
	FeedFactory func() (FeedSource, error)
	ExecFactory func(ExecConfig) (ExecExchange, error)
)

var (
	feedFactories = map[string]FeedFactory{}
	execFactories = map[string]ExecFactory{}
)

func RegisterFeed(name string, f FeedFactory) { feedFactories[name] = f }
func RegisterExec(name string, f ExecFactory) { execFactories[name] = f }

func NewFeed(name string) (FeedSource, error) {
	f, ok := feedFactories[name]
	if !ok {
		return nil, fmt.Errorf("marketmaker: unknown feed source %q (registered: %v)", name, names(feedFactories))
	}
	return f()
}

func NewExec(cfg ExecConfig) (ExecExchange, error) {
	f, ok := execFactories[cfg.Name]
	if !ok {
		return nil, fmt.Errorf("marketmaker: unknown exec exchange %q (registered: %v)", cfg.Name, names(execFactories))
	}
	return f(cfg)
}

func names[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
