package feeds

import (
	"strings"

	"github.com/hnic/trading-floor/pkg/signal"
)

type catalystAlias struct {
	Symbol     string
	Name       string
	EntityType string
	Aliases    []string
}

var catalystAliases = []catalystAlias{
	{Symbol: "RACE", Name: "Ferrari", EntityType: "company", Aliases: []string{"RACE", "Ferrari", "Ferrari N.V.", "Ferrari NV", "Prancing Horse"}},
	{Symbol: "MU", Name: "Micron Technology", EntityType: "company", Aliases: []string{"MU", "Micron", "Micron Technology"}},
	{Symbol: "EWY", Name: "iShares MSCI South Korea ETF", EntityType: "etf", Aliases: []string{"EWY", "South Korea", "Korea stocks", "Korean stocks", "KOSPI"}},
	{Symbol: "NVDA", Name: "NVIDIA", EntityType: "company", Aliases: []string{"NVDA", "NVIDIA"}},
	{Symbol: "AMD", Name: "Advanced Micro Devices", EntityType: "company", Aliases: []string{"AMD", "Advanced Micro Devices"}},
	{Symbol: "TSM", Name: "Taiwan Semiconductor", EntityType: "company", Aliases: []string{"TSM", "TSMC", "Taiwan Semiconductor"}},
	{Symbol: "SMH", Name: "VanEck Semiconductor ETF", EntityType: "etf", Aliases: []string{"SMH", "semiconductors", "chip stocks"}},
	{Symbol: "TSLA", Name: "Tesla", EntityType: "company", Aliases: []string{"TSLA", "Tesla"}},
	{Symbol: "AAPL", Name: "Apple", EntityType: "company", Aliases: []string{"AAPL", "Apple"}},
	{Symbol: "MSFT", Name: "Microsoft", EntityType: "company", Aliases: []string{"MSFT", "Microsoft"}},
	{Symbol: "AMZN", Name: "Amazon", EntityType: "company", Aliases: []string{"AMZN", "Amazon"}},
	{Symbol: "GOOGL", Name: "Alphabet", EntityType: "company", Aliases: []string{"GOOGL", "Google", "Alphabet"}},
	{Symbol: "META", Name: "Meta Platforms", EntityType: "company", Aliases: []string{"META", "Meta Platforms", "Facebook"}},
	{Symbol: "JPM", Name: "JPMorgan Chase", EntityType: "company", Aliases: []string{"JPM", "JPMorgan", "JPMorgan Chase"}},
	{Symbol: "LLY", Name: "Eli Lilly", EntityType: "company", Aliases: []string{"LLY", "Eli Lilly", "Lilly"}},
}

func entitiesFromText(text string, explicitSymbols ...string) []signal.Entity {
	seen := map[string]struct{}{}
	entities := make([]signal.Entity, 0, 4)
	add := func(name, typ, id string) {
		name = strings.TrimSpace(name)
		typ = strings.TrimSpace(typ)
		id = strings.TrimSpace(id)
		if name == "" || typ == "" {
			return
		}
		key := strings.ToLower(typ + ":" + firstNonEmptyLocal(id, name))
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		entities = append(entities, signal.Entity{Name: name, Type: typ, ID: id})
	}

	for _, symbol := range explicitSymbols {
		symbol = strings.TrimSpace(strings.ToUpper(symbol))
		if symbol != "" {
			add(symbol, "instrument", symbol)
		}
	}
	for _, ticker := range extractTickers(text) {
		add(ticker, "instrument", strings.ToUpper(ticker))
	}
	for _, alias := range catalystAliases {
		if !textMatchesAnyAlias(text, alias.Aliases) {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(alias.Symbol))
		add(symbol, "instrument", symbol)
		add(alias.Name, firstNonEmptyLocal(alias.EntityType, "company"), symbol)
	}

	return entities
}

func entitySymbols(entities []signal.Entity) []string {
	seen := map[string]struct{}{}
	symbols := make([]string, 0, len(entities))
	for _, entity := range entities {
		if entity.Type != "instrument" {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(firstNonEmptyLocal(entity.ID, entity.Name)))
		if symbol == "" {
			continue
		}
		if _, ok := seen[symbol]; ok {
			continue
		}
		seen[symbol] = struct{}{}
		symbols = append(symbols, symbol)
	}
	return symbols
}

func textMatchesAnyAlias(text string, aliases []string) bool {
	for _, alias := range aliases {
		if textContainsToken(text, alias) {
			return true
		}
	}
	return false
}

func textContainsToken(text, token string) bool {
	haystack := strings.ToUpper(text)
	needle := strings.ToUpper(strings.TrimSpace(token))
	if haystack == "" || needle == "" {
		return false
	}
	for start := strings.Index(haystack, needle); start >= 0; {
		end := start + len(needle)
		if tokenBoundaryOK(haystack, start-1) && tokenBoundaryOK(haystack, end) {
			return true
		}
		nextOffset := strings.Index(haystack[end:], needle)
		if nextOffset < 0 {
			return false
		}
		start = end + nextOffset
	}
	return false
}

func tokenBoundaryOK(value string, idx int) bool {
	if idx < 0 || idx >= len(value) {
		return true
	}
	c := value[idx]
	return !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'))
}
