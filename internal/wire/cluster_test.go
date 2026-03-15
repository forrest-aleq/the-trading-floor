package wire

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestClustererAssignsRelatedSignalsToSameCluster(t *testing.T) {
	clusterer := NewClusterer(128, 0.88)

	first := NormalizeSignal(signal.Signal{
		ID:        "sig-1",
		Source:    "reuters",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Raw:       []byte(`{"title":"Apple plans supplier expansion in India","description":"The company is shifting more assembly capacity into India"}`),
	})
	second := NormalizeSignal(signal.Signal{
		ID:        "sig-2",
		Source:    "ft",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Raw:       []byte(`{"title":"Apple expands India supplier footprint","description":"Assembly capacity continues moving toward India suppliers"}`),
	})

	first = clusterer.Assign(first)
	second = clusterer.Assign(second)

	if first.ClusterID == "" {
		t.Fatal("expected first signal to receive cluster id")
	}
	if second.ClusterID != first.ClusterID {
		t.Fatalf("expected related signals in same cluster, got %s vs %s", first.ClusterID, second.ClusterID)
	}
	if len(second.RelatedSignalIDs) == 0 || second.RelatedSignalIDs[0] != "sig-1" {
		t.Fatalf("expected second signal to reference prior member, got %+v", second.RelatedSignalIDs)
	}
}
