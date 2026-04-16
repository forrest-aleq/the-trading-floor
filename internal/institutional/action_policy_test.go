package institutional

import "testing"

func TestActiveActionPolicyHasNamedActions(t *testing.T) {
	policy := activeActionPolicy()
	if len(policy.Actions) == 0 {
		t.Fatal("expected action policy actions")
	}

	foundInvestigate := false
	for _, action := range policy.Actions {
		if action.Name == "" {
			t.Fatalf("expected normalized action name, got %+v", action)
		}
		if action.Name == "investigate" {
			foundInvestigate = true
		}
	}
	if !foundInvestigate {
		t.Fatalf("expected investigate action in policy, got %+v", policy.Actions)
	}
}
