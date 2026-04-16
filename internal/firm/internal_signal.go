package firm

import (
	"fmt"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

func buildInternalSignal(origin signal.Signal, thesis *model.Thesis, originDesk string) (signal.Signal, bool) {
	policy := activeCollaborationPolicy()
	if thesis == nil || thesis.Conviction < policy.MinPublishConviction {
		return signal.Signal{}, false
	}
	originIsInternal := isInternalSignal(origin)
	originMessage, _ := model.DecodeColleagueMessage(origin.Raw)
	if originIsInternal && originMessage.InternalDepth >= policy.MaxInternalDepth {
		return signal.Signal{}, false
	}

	targets := downstreamDomainsForDesk(thesis.Domain)
	kind := model.ColleagueMessageProposal
	replyToMessageID := ""
	parentThesisID := ""
	rootThesisID := thesis.ID
	threadID := model.NewColleagueThreadID(thesis.ID)
	requestedAction := policy.ProposalAction
	depth := 1
	if originIsInternal {
		kind = model.ColleagueMessageReply
		threadID = firstNonEmptyInternal(originMessage.ThreadID, model.NewColleagueThreadID(thesis.ID))
		replyToMessageID = originMessage.MessageID
		parentThesisID = firstNonEmptyInternal(originMessage.ThesisID, originMessage.ParentThesisID)
		rootThesisID = firstNonEmptyInternal(originMessage.RootThesisID, originMessage.ThesisID, thesis.ID)
		requestedAction = policy.ReplyAction
		depth = originMessage.InternalDepth + 1
		if originMessage.OriginDomain != "" {
			targets = []string{originMessage.OriginDomain}
		}
	}
	if len(targets) == 0 {
		return signal.Signal{}, false
	}

	text := internalSignalSummary(kind, thesis)
	payload := model.ColleagueMessage{
		ThreadID:         threadID,
		MessageID:        model.NewColleagueMessageID(thesis.ID),
		ReplyToMessageID: replyToMessageID,
		OriginDesk:       originDesk,
		OriginDomain:     thesis.Domain,
		OriginSignalID:   origin.ID,
		ThesisID:         thesis.ID,
		ParentThesisID:   parentThesisID,
		RootThesisID:     rootThesisID,
		TargetDomains:    targets,
		Structure:        firstNonEmptyInternal(thesis.Structure, "single"),
		Strategy:         thesis.Strategy,
		Conviction:       thesis.Conviction,
		InternalDepth:    depth,
		Kind:             kind,
		RequestedAction:  requestedAction,
		Subject:          firstNonEmptyInternal(thesis.DisplaySymbol(), thesis.Domain),
		Summary:          text,
		DisplaySymbol:    thesis.DisplaySymbol(),
	}

	return signal.Signal{
		ID:                    "internal-" + thesis.ID,
		Source:                "internal/" + originDesk,
		Type:                  signal.TypeAlternative,
		Category:              thesis.Domain,
		Timestamp:             time.Now().UTC(),
		Urgency:               maxInternal(thesis.Conviction, 0.55),
		Strength:              thesis.Conviction,
		Direction:             signalDirectionFromTradeDirection(thesis.Direction),
		Entities:              internalSignalEntities(thesis),
		Languages:             []string{"en"},
		Raw:                   payload.Encode(),
		OriginalText:          text,
		Translated:            text,
		TranslationProvider:   "internal_identity",
		TranslationConfidence: 1,
	}, true
}

func internalSignalSummary(kind model.ColleagueMessageKind, thesis *model.Thesis) string {
	verb := "Internal thesis"
	if kind == model.ColleagueMessageReply {
		verb = "Colleague reply"
	}
	return fmt.Sprintf(
		"%s from %s desk: %s %s structure=%s strategy=%s conviction=%.2f",
		verb,
		thesis.Domain,
		thesis.DisplaySymbol(),
		thesis.Direction,
		firstNonEmptyInternal(thesis.Structure, "single"),
		thesis.Strategy,
		thesis.Conviction,
	)
}

func downstreamDomainsForDesk(domain string) []string {
	policy := activeCollaborationPolicy()
	set := newDomainSet()
	set.add(policy.DownstreamDomains[strings.TrimSpace(strings.ToLower(domain))]...)
	return set.values()
}

func internalTargetDomains(sig signal.Signal) []string {
	payload, ok := model.DecodeColleagueMessage(sig.Raw)
	if !ok {
		return nil
	}
	set := newDomainSet()
	set.add(payload.TargetDomains...)
	return set.values()
}

func internalOriginDesk(sig signal.Signal) string {
	payload, ok := model.DecodeColleagueMessage(sig.Raw)
	if !ok {
		return ""
	}
	return strings.TrimSpace(payload.OriginDesk)
}

func internalSignalDepth(sig signal.Signal) int {
	payload, ok := model.DecodeColleagueMessage(sig.Raw)
	if !ok {
		return 0
	}
	if payload.InternalDepth < 0 {
		return 0
	}
	return payload.InternalDepth
}

func isInternalSignal(sig signal.Signal) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(sig.Source)), "internal/")
}

func internalSignalEntities(thesis *model.Thesis) []signal.Entity {
	if thesis == nil {
		return nil
	}
	instruments := thesis.ExecutionInstruments()
	entities := make([]signal.Entity, 0, len(instruments))
	seen := make(map[string]struct{}, len(instruments))
	for _, inst := range instruments {
		symbol := strings.TrimSpace(inst.Symbol)
		if symbol == "" {
			continue
		}
		if _, ok := seen[symbol]; ok {
			continue
		}
		seen[symbol] = struct{}{}
		entities = append(entities, signal.Entity{Name: symbol, Type: "instrument"})
	}
	return entities
}

func signalDirectionFromTradeDirection(direction model.TradeDirection) signal.Direction {
	if direction == model.Short {
		return signal.Bearish
	}
	return signal.Bullish
}

func maxInternal(values ...float64) float64 {
	maximum := 0.0
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func firstNonEmptyInternal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
