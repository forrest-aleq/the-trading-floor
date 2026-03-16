package entityresolve

import (
	"strings"
	"unicode"

	"github.com/hnic/trading-floor/pkg/signal"
)

type Resolved struct {
	CanonicalID   string
	CanonicalName string
	Type          string
	Confidence    float64
	Language      string
	Script        string
	Alias         string
}

type definition struct {
	ID      string
	Name    string
	Type    string
	Aliases []string
}

var aliasIndex = buildAliasIndex([]definition{
	{ID: "company:NVDA", Name: "NVIDIA", Type: "company", Aliases: []string{"NVDA", "NVIDIA", "英伟达"}},
	{ID: "company:AAPL", Name: "Apple", Type: "company", Aliases: []string{"AAPL", "APPLE", "苹果"}},
	{ID: "company:MSFT", Name: "Microsoft", Type: "company", Aliases: []string{"MSFT", "MICROSOFT", "微软"}},
	{ID: "company:AMZN", Name: "Amazon", Type: "company", Aliases: []string{"AMZN", "AMAZON", "亚马逊"}},
	{ID: "company:GOOGL", Name: "Alphabet", Type: "company", Aliases: []string{"GOOGL", "GOOGLE", "ALPHABET", "谷歌", "字母表"}},
	{ID: "country:IRAN", Name: "Iran", Type: "country", Aliases: []string{"IRAN", "ایران", "ايران", "伊朗"}},
	{ID: "country:RUSSIA", Name: "Russia", Type: "country", Aliases: []string{"RUSSIA", "РОССИЯ", "روسيا", "俄罗斯"}},
	{ID: "country:CHINA", Name: "China", Type: "country", Aliases: []string{"CHINA", "中国", "中國", "PRC"}},
	{ID: "country:SAUDI_ARABIA", Name: "Saudi Arabia", Type: "country", Aliases: []string{"SAUDI ARABIA", "KSA", "السعودية"}},
	{ID: "commodity:CRUDE_OIL", Name: "Crude Oil", Type: "commodity", Aliases: []string{"CRUDE", "CRUDE OIL", "OIL", "原油", "نفط"}},
	{ID: "commodity:GOLD", Name: "Gold", Type: "commodity", Aliases: []string{"GOLD", "GLD", "黄金", "ذهب"}},
	{ID: "index:VIX", Name: "VIX", Type: "index", Aliases: []string{"VIX", "VOLATILITY INDEX", "恐慌指数"}},
})

func Resolve(entity signal.Entity, language string) Resolved {
	name := strings.TrimSpace(entity.Name)
	typ := normalizeType(entity.Type)
	if typ == "" {
		typ = "unknown"
	}

	if def, ok := aliasIndex[normalizeAlias(name)]; ok {
		return Resolved{
			CanonicalID:   def.ID,
			CanonicalName: def.Name,
			Type:          def.Type,
			Confidence:    0.95,
			Language:      normalizeLanguage(language),
			Script:        DetectScript(name),
			Alias:         name,
		}
	}

	if strings.TrimSpace(entity.ID) != "" {
		return Resolved{
			CanonicalID:   typ + ":" + strings.ToUpper(strings.TrimSpace(entity.ID)),
			CanonicalName: name,
			Type:          typ,
			Confidence:    0.75,
			Language:      normalizeLanguage(language),
			Script:        DetectScript(name),
			Alias:         name,
		}
	}

	canonicalName := strings.ToUpper(strings.Join(strings.Fields(name), " "))
	return Resolved{
		CanonicalID:   typ + ":" + canonicalName,
		CanonicalName: name,
		Type:          typ,
		Confidence:    0.55,
		Language:      normalizeLanguage(language),
		Script:        DetectScript(name),
		Alias:         name,
	}
}

func NormalizeKey(name string) string {
	return normalizeAlias(name)
}

func DetectScript(text string) string {
	for _, r := range strings.TrimSpace(text) {
		switch {
		case unicode.In(r, unicode.Han):
			return "han"
		case unicode.In(r, unicode.Arabic):
			return "arabic"
		case unicode.In(r, unicode.Cyrillic):
			return "cyrillic"
		case unicode.In(r, unicode.Hebrew):
			return "hebrew"
		case unicode.In(r, unicode.Devanagari):
			return "devanagari"
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			return "japanese"
		case unicode.In(r, unicode.Hangul):
			return "hangul"
		case unicode.In(r, unicode.Thai):
			return "thai"
		case unicode.In(r, unicode.Latin):
			return "latin"
		}
	}
	return "unknown"
}

func buildAliasIndex(definitions []definition) map[string]definition {
	index := make(map[string]definition)
	for _, def := range definitions {
		for _, alias := range def.Aliases {
			index[normalizeAlias(alias)] = def
		}
	}
	return index
}

func normalizeAlias(value string) string {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return ""
	}
	value = strings.Join(strings.Fields(value), " ")
	replacer := strings.NewReplacer(
		".", "",
		",", "",
		":", " ",
		";", " ",
		"/", " ",
		"-", " ",
		"_", " ",
		"(", " ",
		")", " ",
	)
	value = replacer.Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func normalizeType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	return value
}

func normalizeLanguage(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "und"
	}
	return value
}
