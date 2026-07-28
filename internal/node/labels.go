package node

import "sort"

// LabelSummary is one node-label key and the distinct values seen across the
// fleet. Reported to Sunshine so the config UI can offer REAL Kubernetes label
// keys instead of inferring them from Datadog host tags.
//
// That inference was the bug this exists to kill: Datadog normalizes a label key
// when it turns it into a tag (`karpenter.sh/nodepool` arrives as
// `karpenter_nodepool`), and a selector built from the normalized form never
// matches a real node — the pool resolves empty and the autopilot sits inert
// with no visible error. The cluster is the only authority on what its own
// labels are called, and the controller is the only thing standing in it.
type LabelSummary struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

const (
	// maxLabelKeys bounds the payload. Well above any real pool-label count.
	maxLabelKeys = 60
	// maxValuesPerLabel drops per-node identity labels (hostname, instance id,
	// internal IP): a key with more distinct values than this cannot be
	// describing a pool, and enumerating it would blow up the payload on a big
	// fleet.
	maxValuesPerLabel = 12
)

// SummarizeLabels collects the distinct values per label key across nodes.
//
// This applies SIZE bounds only — it deliberately does NOT decide which labels
// are interesting. Sunshine already owns that judgement (noise keys, pool-name
// hints, ranking) and duplicating it here would let the two drift apart, with
// the cluster silently withholding a key the server would have wanted. The
// controller answers "what labels exist"; the server answers "which ones
// matter".
//
// Output is fully sorted, so an unchanged fleet produces a byte-identical
// summary every reconcile rather than a new one each time.
func SummarizeLabels(nodes []Node) []LabelSummary {
	byKey := make(map[string]map[string]struct{})
	for _, n := range nodes {
		for k, v := range n.Labels {
			if _, ok := byKey[k]; !ok {
				byKey[k] = make(map[string]struct{})
			}
			byKey[k][v] = struct{}{}
		}
	}

	keys := make([]string, 0, len(byKey))
	for k, vals := range byKey {
		if len(vals) > maxValuesPerLabel {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > maxLabelKeys {
		keys = keys[:maxLabelKeys]
	}

	// Never nil: an empty summary must marshal as `[]`, not `null`. A nil slice
	// would say "this controller cannot report labels" when it means "this
	// cluster has none worth reporting" — the same null-vs-empty conflation that
	// cost us the whole audit trail in sunnysystems-sunshine#641.
	out := make([]LabelSummary, 0, len(keys))
	for _, k := range keys {
		vals := make([]string, 0, len(byKey[k]))
		for v := range byKey[k] {
			vals = append(vals, v)
		}
		sort.Strings(vals)
		out = append(out, LabelSummary{Key: k, Values: vals})
	}
	return out
}
