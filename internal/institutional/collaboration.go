package institutional

import (
	"fmt"
	"math"
	"strings"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type RelationshipLookup func(originDesk, originDomain string) (*model.DeskRelationshipBelief, bool)

func IsInternalSignal(sig signal.Signal) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(sig.Source)), "internal/")
}

func CollaborationInputForSignal(sig signal.Signal, lookup RelationshipLookup) *model.CollaborationInput {
	if !IsInternalSignal(sig) {
		return nil
	}
	message, ok := model.DecodeColleagueMessage(sig.Raw)
	if !ok {
		return nil
	}
	input := model.CollaborationInputFromMessage(message)
	if input == nil {
		return nil
	}
	if lookup != nil && input.OriginDesk != "" {
		if peer, ok := lookup(input.OriginDesk, input.OriginDomain); ok && peer != nil {
			input.RelationshipTrust = peer.Trust
			input.RelationshipConfidence = peer.Confidence
			input.RelationshipHealth = peer.RelationshipHealth
			input.RecoveryScore = peer.RecoveryScore
		}
	}
	if sig.Appraisal != nil {
		input.AppraisalClass = sig.Appraisal.ViolationClass
		input.FaceThreatScore = sig.Appraisal.FaceThreatScore
		input.SocialCost = sig.Appraisal.SocialCost
		if sig.Appraisal.RelationshipHealth > 0 {
			input.RelationshipHealth = sig.Appraisal.RelationshipHealth
		}
	}
	return input
}

func AugmentSignalWithCollaborationContext(sig signal.Signal, input *model.CollaborationInput) signal.Signal {
	if input == nil {
		return sig
	}
	contextBlock := BuildCollaborationContext(input, "  ")
	if sig.InstitutionalContext == "" {
		sig.InstitutionalContext = contextBlock
	} else if contextBlock != "" {
		sig.InstitutionalContext = strings.TrimSpace(sig.InstitutionalContext) + "\n" + contextBlock
	}
	return sig
}

func ApplyCollaborationInput(thesis *model.Thesis, input *model.CollaborationInput, trustBaseline, trustWeight float64) {
	if thesis == nil || input == nil {
		return
	}
	if input.RelationshipTrust > 0 {
		adjustment := (input.RelationshipTrust - trustBaseline) * trustWeight
		thesis.Conviction = math.Max(0, math.Min(1, thesis.Conviction+adjustment))
	}
	thesis.CollaborationInput = input
	if input.Summary != "" && !hasEvidenceSource(thesis, "colleague:"+input.OriginDesk) {
		weight := math.Max(0.25, math.Min(0.95, input.RelationshipTrust))
		thesis.Evidence = append(thesis.Evidence, model.Evidence{
			Source:  "colleague:" + input.OriginDesk,
			Content: fmt.Sprintf("Internal %s from %s desk. requested_action=%s peer_trust=%.2f peer_confidence=%.2f summary=%s", input.Kind, input.OriginDesk, input.RequestedAction, input.RelationshipTrust, input.RelationshipConfidence, input.Summary),
			Weight:  weight,
		})
	}
}

func hasEvidenceSource(thesis *model.Thesis, source string) bool {
	if thesis == nil || source == "" {
		return false
	}
	for _, item := range thesis.Evidence {
		if item.Source == source {
			return true
		}
	}
	return false
}
