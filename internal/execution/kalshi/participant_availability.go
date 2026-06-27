package kalshi

import (
	"fmt"
	"strings"

	"github.com/hnic/trading-floor/pkg/model"
)

func validateParticipantAvailability(thesis *model.Thesis) error {
	if thesis == nil || !thesis.PrimaryInstrument().IsKalshi() {
		return nil
	}

	dependenceText := participantDependenceText(thesis)
	if !looksParticipantDependent(dependenceText) {
		return nil
	}
	normalized := normalizeAvailabilityText(explicitParticipantAvailabilityText(thesis))
	if hasParticipantAvailabilityBlock(normalized) {
		return fmt.Errorf("kalshi_participant_availability_blocked: player-dependent market has unavailable or unresolved participant evidence")
	}
	if hasParticipantAvailabilityConfirmation(normalized) {
		return nil
	}
	return fmt.Errorf("kalshi_participant_availability_unverified: player-dependent Kalshi market requires live participant availability evidence")
}

func participantDependenceText(thesis *model.Thesis) string {
	if thesis == nil {
		return ""
	}
	parts := []string{
		thesis.Strategy,
		thesis.Structure,
		thesis.DisplaySymbol(),
	}
	for _, item := range thesis.Evidence {
		parts = append(parts, item.Source, item.Content)
	}
	for _, arg := range thesis.CounterArgs {
		parts = append(parts, arg)
	}
	if thesis.MarketContext != nil {
		parts = append(parts, thesis.MarketContext.Notes...)
	}
	if thesis.SurpriseAssessment != nil {
		parts = append(parts, thesis.SurpriseAssessment.Summary)
	}
	return strings.Join(parts, " | ")
}

func explicitParticipantAvailabilityText(thesis *model.Thesis) string {
	if thesis == nil {
		return ""
	}
	parts := []string{}
	for _, item := range thesis.Evidence {
		if isExplicitParticipantAvailability(item.Source, item.Content) {
			parts = append(parts, item.Source, item.Content)
		}
	}
	if thesis.MarketContext != nil {
		for _, note := range thesis.MarketContext.Notes {
			if isExplicitParticipantAvailability("", note) {
				parts = append(parts, note)
			}
		}
	}
	return strings.Join(parts, " | ")
}

func isExplicitParticipantAvailability(source, content string) bool {
	text := normalizeAvailabilityText(source + " " + content)
	for _, marker := range []string{
		"participant_availability:",
		"participant availability:",
		"official lineup",
		"confirmed lineup",
		"starting xi",
		"named in squad",
		"roster match",
		"espn_roster_match",
		"source=espn",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	for _, marker := range []string{"availability", "lineup", "roster"} {
		if strings.Contains(normalizeAvailabilityText(source), marker) {
			return true
		}
	}
	return false
}

func looksParticipantDependent(text string) bool {
	text = normalizeAvailabilityText(text)
	if text == "" {
		return false
	}
	terms := []string{
		"anytime goalscorer",
		"first goalscorer",
		"goal scorer",
		"goalscorer",
		"player prop",
		"player points",
		"player rebounds",
		"player assists",
		"player threes",
		"player shots",
		"shots on target",
		"player saves",
		"passing yards",
		"rushing yards",
		"receiving yards",
		"touchdown scorer",
		"anytime touchdown",
		"strikeouts",
		"home run",
		"stolen base",
		"total bases",
	}
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func hasParticipantAvailabilityBlock(text string) bool {
	blocks := []string{
		"participant_availability: blocked",
		"participant availability: blocked",
		"participant_availability: unresolved",
		"participant availability: unresolved",
		"active=false",
		"available=false",
		"status=out",
		"status=inactive",
		"status=suspended",
		"status=injured",
		"status=unresolved",
		"player_not_found",
		"event_not_found",
		"not in squad",
		"not named in squad",
		"not playing",
		"ruled out",
		"scratched",
		"inactive",
		"suspended",
	}
	for _, block := range blocks {
		if strings.Contains(text, block) {
			return true
		}
	}
	return false
}

func hasParticipantAvailabilityConfirmation(text string) bool {
	confirmations := []string{
		"participant_availability: confirmed",
		"participant availability: confirmed",
		"official lineup confirms",
		"confirmed lineup",
		"starting xi confirms",
		"named in starting xi",
		"named in squad",
		"active=true",
		"available=true",
	}
	for _, confirmation := range confirmations {
		if strings.Contains(text, confirmation) {
			return true
		}
	}
	return false
}

func normalizeAvailabilityText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
