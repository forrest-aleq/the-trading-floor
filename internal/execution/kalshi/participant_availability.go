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
	availability := thesis.ParticipantAvailability
	if availability == nil {
		return fmt.Errorf("kalshi_participant_availability_unverified: player-dependent Kalshi market requires structured ESPN participant availability evidence")
	}
	if !strings.EqualFold(strings.TrimSpace(availability.Source), "espn") {
		return fmt.Errorf("kalshi_participant_availability_unverified: player-dependent Kalshi market requires structured ESPN participant availability evidence")
	}
	if participantAvailabilityBlocked(availability) {
		return fmt.Errorf("kalshi_participant_availability_blocked: player-dependent market has unavailable or unresolved participant evidence")
	}
	availabilityText := structuredParticipantAvailabilityText(availability)
	if requiresParticipantStarter(dependenceText, availabilityText) && (availability.Starter == nil || !*availability.Starter) {
		return fmt.Errorf("kalshi_participant_availability_blocked: player-dependent market requires confirmed starter")
	}
	if participantAvailabilityConfirmed(availability) {
		return nil
	}
	return fmt.Errorf("kalshi_participant_availability_unverified: player-dependent Kalshi market requires structured ESPN participant availability evidence")
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

func participantAvailabilityBlocked(availability *model.ParticipantAvailability) bool {
	if availability == nil {
		return false
	}
	status := normalizeAvailabilityText(availability.Status)
	reason := normalizeAvailabilityText(availability.Reason)
	for _, blocked := range []string{
		"blocked",
		"unresolved",
		"out",
		"inactive",
		"suspended",
		"injured",
	} {
		if status == blocked || strings.Contains(reason, blocked) {
			return true
		}
	}
	if availability.Active != nil && !*availability.Active {
		return true
	}
	return false
}

func participantAvailabilityConfirmed(availability *model.ParticipantAvailability) bool {
	if availability == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(availability.Source), "espn") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(availability.Status), "confirmed") {
		return false
	}
	return availability.Active != nil && *availability.Active
}

func structuredParticipantAvailabilityText(availability *model.ParticipantAvailability) string {
	if availability == nil {
		return ""
	}
	parts := []string{
		"participant_availability: " + strings.ToLower(strings.TrimSpace(availability.Status)),
		"source=" + strings.ToLower(strings.TrimSpace(availability.Source)),
		"league=" + strings.ToLower(strings.TrimSpace(availability.League)),
	}
	if availability.Active != nil {
		parts = append(parts, fmt.Sprintf("active=%t", *availability.Active))
	}
	if availability.Starter != nil {
		parts = append(parts, fmt.Sprintf("starter=%t", *availability.Starter))
	}
	return strings.Join(parts, " ")
}

func requiresParticipantStarter(dependenceText, availabilityText string) bool {
	if !readBoolEnv("KALSHI_SPORTS_REQUIRE_STARTER_FOR_SOCCER_PLAYER_PROPS", true) {
		return false
	}
	if !hasSoccerAvailabilityEvidence(availabilityText) {
		return false
	}
	text := normalizeAvailabilityText(dependenceText)
	for _, term := range []string{
		"goalscorer",
		"goal scorer",
		"shots on target",
		"player assists",
	} {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func hasSoccerAvailabilityEvidence(text string) bool {
	text = normalizeAvailabilityText(text)
	for _, marker := range []string{
		"league=fifa.",
		"league=uefa.",
		"league=eng.1",
		"league=esp.1",
		"league=ita.1",
		"league=ger.1",
		"league=fra.1",
		"league=usa.1",
		"sport=soccer",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func normalizeAvailabilityText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
