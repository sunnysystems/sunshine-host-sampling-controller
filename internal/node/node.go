// Package node models cluster nodes and pool classification, independent of any
// Kubernetes client (so it is unit-testable offline).
package node

import (
	"strings"
	"time"
)

// Node is a minimal view of a cluster node.
type Node struct {
	Name      string
	Labels    map[string]string
	CreatedAt time.Time
}

// MatchSelector reports whether the labels satisfy a simple `key=value`
// selector (the common case + what the cluster config declares). Anything more
// complex is treated as no-match — deliberately conservative.
func MatchSelector(labels map[string]string, selector string) bool {
	i := strings.Index(selector, "=")
	if i <= 0 {
		return false
	}
	key := strings.TrimSpace(selector[:i])
	val := strings.TrimSpace(selector[i+1:])
	if key == "" || val == "" {
		return false
	}
	return labels[key] == val
}

// Pools splits nodes into the fixed fleet (stable) and the surge pool. A node
// matching the surge selector goes to surge; otherwise a stable-selector match
// goes to stable; anything else is untracked and left monitored.
type Pools struct {
	Stable []Node
	Surge  []Node
}

// Classify partitions nodes by the stable and surge selectors.
func Classify(nodes []Node, stableSelector, surgeSelector string) Pools {
	var p Pools
	for _, n := range nodes {
		switch {
		case MatchSelector(n.Labels, surgeSelector):
			p.Surge = append(p.Surge, n)
		case MatchSelector(n.Labels, stableSelector):
			p.Stable = append(p.Stable, n)
		}
	}
	return p
}
