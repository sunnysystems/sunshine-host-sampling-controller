package node

import (
	"sort"
	"time"
)

// maxReportedAges bounds the age list. Mirrors MAX_REPORTED_NODE_AGES on the
// Sunshine side; the two must move together or the server drops the payload.
const maxReportedAges = 400

// NodeAge is one node's real birth, as Kubernetes records it.
type NodeAge struct {
	// Name is the Kubernetes node name. Sunshine's inventory names the same
	// machine differently — its monitoring vendor appends the cluster — and the
	// server owns that translation. Sending anything but the plain node name
	// would move that knowledge into the cluster, where it does not belong.
	Name string `json:"name"`
	// CreatedAt is metadata.creationTimestamp, RFC3339 in UTC.
	CreatedAt string `json:"createdAt"`
}

// NodeAges reports when each node was created (sunnysystems-sunshine#752).
//
// Sunshine ranks nodes by age to decide which are the surge ones, and until now
// it could only use "the first time our hourly census saw this host" — not the
// node's age. That estimate cannot see backwards, so every node predating the
// census carries one identical timestamp and their relative order is arbitrary.
// The cluster has the real answer for free, in every node it already lists.
//
// Over the cap this returns nil, NOT a prefix. A truncated list silently
// re-ranks the fleet, and a partial ranking is worse than none because it looks
// complete — the same rule SummarizeLabels applies to an over-wide label key,
// which it drops whole rather than sampling. Note this differs deliberately
// from SampledNodes, which truncates: that one is a description of what
// happened, where a prefix is still true.
//
// Nil is also the honest answer for an empty fleet: absent means "no ranking
// from here", and a cluster with no nodes has none to give. Unlike the surge
// echo, there is no third state to protect — "I see no nodes" and "I cannot
// tell you" lead the server to the same fallback.
//
// Sorted by name so an unchanged fleet produces a byte-identical payload every
// reconcile instead of a new one each time.
func NodeAges(nodes []Node) []NodeAge {
	if len(nodes) == 0 || len(nodes) > maxReportedAges {
		return nil
	}
	out := make([]NodeAge, 0, len(nodes))
	for _, n := range nodes {
		// A node with no creation timestamp is not a node with age zero. The
		// zero time would marshal as year 1 and rank it as the eldest — the
		// single most dangerous thing this list could say, since the eldest are
		// exactly the ones the controller keeps monitored.
		if n.CreatedAt.IsZero() {
			continue
		}
		out = append(out, NodeAge{
			Name:      n.Name,
			CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
