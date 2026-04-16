package firm

import "testing"

func TestActiveCollaborationPolicyHasDownstreamRules(t *testing.T) {
	policy := activeCollaborationPolicy()
	if policy.MinPublishConviction <= 0 || policy.MinPublishConviction > 1 {
		t.Fatalf("unexpected min publish conviction %+v", policy)
	}
	if policy.MaxInternalDepth <= 0 {
		t.Fatalf("unexpected max internal depth %+v", policy)
	}
	if len(policy.DownstreamDomains["macro"]) == 0 {
		t.Fatalf("expected macro downstream domains, got %+v", policy.DownstreamDomains)
	}
}
