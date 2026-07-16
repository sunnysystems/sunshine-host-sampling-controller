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

// MatchAnySelector reports whether the labels satisfy ANY of the selectors — a
// cluster commonly has several surge nodepools. An empty list matches nothing,
// which keeps an unconfigured cluster fail-open.
func MatchAnySelector(labels map[string]string, selectors []string) bool {
	for _, s := range selectors {
		if MatchSelector(labels, s) {
			return true
		}
	}
	return false
}

// Pools splits nodes into the fixed fleet (stable) and the surge pool. Only the
// surge pool is ever actuated; stable is reported, never touched.
type Pools struct {
	Stable []Node
	Surge  []Node
}

// Classify partitions nodes by the stable and surge selectors.
//
// A node matching any surge selector goes to surge. Everything else is stable:
// leaving stableSelector empty is the normal case, and it makes the reported
// split match what the fleet actually does — a node matching no surge selector
// is left monitored, whether or not anyone declared it. A non-empty
// stableSelector narrows the reported stable pool to that selector, leaving
// unlabelled nodes out of both tallies; it changes reporting only.
//
// Surge is tested FIRST, so a selector named as both stable and surge resolves
// to surge. The server refuses that overlap at write time — this ordering is
// only the last line of defence for a config written before it did.
func Classify(nodes []Node, stableSelector string, surgeSelectors []string) Pools {
	var p Pools
	// Nothing declared at all (an unconfigured, fail-open policy) describes no
	// pools, so report none. Without this, the derived-stable rule below would
	// swing the stable gauge from 0 to the whole fleet the moment a controller
	// loses its policy — a reporting artefact that looks like a real event.
	if len(surgeSelectors) == 0 && stableSelector == "" {
		return p
	}
	for _, n := range nodes {
		switch {
		case MatchAnySelector(n.Labels, surgeSelectors):
			p.Surge = append(p.Surge, n)
		case stableSelector == "" || MatchSelector(n.Labels, stableSelector):
			p.Stable = append(p.Stable, n)
		}
	}
	return p
}
