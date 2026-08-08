package node

import (
	"encoding/json"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNodeAgesReportsCreationTimes(t *testing.T) {
	got := NodeAges([]Node{
		{Name: "b", CreatedAt: at("2026-08-05T22:03:00Z")},
		{Name: "a", CreatedAt: at("2026-05-01T10:00:00Z")},
	})
	if len(got) != 2 {
		t.Fatalf("want 2 ages, got %d", len(got))
	}
	// Sorted by name so an unchanged fleet reports byte-identically.
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("not sorted by name: %+v", got)
	}
	if got[0].CreatedAt != "2026-05-01T10:00:00Z" {
		t.Fatalf("want RFC3339 UTC, got %q", got[0].CreatedAt)
	}
}

func TestNodeAgesNormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("BRT", -3*60*60)
	got := NodeAges([]Node{
		{Name: "a", CreatedAt: time.Date(2026, 8, 5, 19, 3, 0, 0, zone)},
	})
	if got[0].CreatedAt != "2026-08-05T22:03:00Z" {
		t.Fatalf("want UTC, got %q", got[0].CreatedAt)
	}
}

func TestNodeAgesDropsWholeListOverCap(t *testing.T) {
	// A prefix would silently re-rank the fleet, and a partial ranking is worse
	// than none because it looks complete.
	nodes := make([]Node, maxReportedAges+1)
	for i := range nodes {
		nodes[i] = Node{Name: string(rune('a' + i%26)), CreatedAt: at("2026-05-01T10:00:00Z")}
	}
	if got := NodeAges(nodes); got != nil {
		t.Fatalf("want nil over the cap, got %d entries", len(got))
	}
}

func TestNodeAgesKeepsExactlyTheCap(t *testing.T) {
	nodes := make([]Node, maxReportedAges)
	for i := range nodes {
		nodes[i] = Node{Name: string(rune('a' + i%26)), CreatedAt: at("2026-05-01T10:00:00Z")}
	}
	if got := NodeAges(nodes); len(got) != maxReportedAges {
		t.Fatalf("want %d at the cap, got %d", maxReportedAges, len(got))
	}
}

func TestNodeAgesSkipsNodesWithoutATimestamp(t *testing.T) {
	// The zero time marshals as year 1 and would rank that node as the eldest —
	// and the eldest are exactly the ones the controller keeps monitored.
	got := NodeAges([]Node{
		{Name: "known", CreatedAt: at("2026-05-01T10:00:00Z")},
		{Name: "unknown"},
	})
	if len(got) != 1 || got[0].Name != "known" {
		t.Fatalf("want only the dated node, got %+v", got)
	}
}

func TestNodeAgesIsNilWhenNothingCanBeSaid(t *testing.T) {
	if got := NodeAges(nil); got != nil {
		t.Fatalf("want nil for an empty fleet, got %+v", got)
	}
	if got := NodeAges([]Node{{Name: "a"}}); got != nil {
		t.Fatalf("want nil when no node has a timestamp, got %+v", got)
	}
}

func TestNodeAgesOmittedFromJSONWhenNil(t *testing.T) {
	// The whole point of nil: absent means "no ranking from here", and the
	// server falls back to its census rather than trusting an empty list.
	type wrapper struct {
		NodeAges []NodeAge `json:"nodeAges,omitempty"`
	}
	b, err := json.Marshal(wrapper{NodeAges: NodeAges(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}" {
		t.Fatalf("want the field omitted, got %s", b)
	}
}
