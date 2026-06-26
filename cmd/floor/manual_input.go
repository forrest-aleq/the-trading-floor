package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/pkg/signal"
)

type manualInputServerConfig struct {
	Enabled bool
	Bind    string
	Token   string
	Publish func(context.Context, signal.Signal) error
}

type manualInputPayload struct {
	Text      string  `json:"text"`
	Category  string  `json:"category"`
	Direction string  `json:"direction"`
	Symbols   string  `json:"symbols"`
	Source    string  `json:"source"`
	Urgency   float64 `json:"urgency"`
	Strength  float64 `json:"strength"`
}

func startManualInputServer(ctx context.Context, cfg manualInputServerConfig) *http.Server {
	if !cfg.Enabled || cfg.Publish == nil {
		return nil
	}
	bind := strings.TrimSpace(cfg.Bind)
	if bind == "" {
		bind = "127.0.0.1:8787"
	}

	log := slog.Default().With("component", "manual_input")
	server := &http.Server{
		Addr:              bind,
		Handler:           newManualInputHandler(cfg.Publish, cfg.Token, log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("manual input server started",
			"bind", bind,
			"url", "http://"+bind+"/",
			"token_required", strings.TrimSpace(cfg.Token) != "",
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("manual input server failed", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("manual input server shutdown failed", "error", err)
		}
	}()

	return server
}

func newManualInputHandler(publish func(context.Context, signal.Signal) error, token string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default().With("component", "manual_input")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(manualInputPage(token)))
	})
	mux.HandleFunc("/manual-signal", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !manualInputAuthorized(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		payload, err := readManualInputPayload(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sig, err := buildManualSignal(payload, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		publishCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		err = publish(publishCtx, sig)
		cancel()
		if err != nil {
			log.Warn("manual catalyst publish failed", "signal_id", sig.ID, "error", err)
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}

		log.Info("manual catalyst injected",
			"signal_id", sig.ID,
			"category", sig.Category,
			"direction", sig.Direction,
			"entities", len(sig.Entities),
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":  true,
			"signal_id": sig.ID,
			"source":    sig.Source,
			"category":  sig.Category,
			"direction": sig.Direction,
			"entities":  sig.Entities,
		})
	})
	return mux
}

func manualInputAuthorized(r *http.Request, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("X-Manual-Input-Token")) == token {
		return true
	}
	if strings.TrimSpace(r.URL.Query().Get("token")) == token {
		return true
	}
	if err := r.ParseForm(); err == nil && strings.TrimSpace(r.Form.Get("token")) == token {
		return true
	}
	return false
}

func readManualInputPayload(r *http.Request) (manualInputPayload, error) {
	var payload manualInputPayload
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return payload, fmt.Errorf("invalid JSON payload: %w", err)
		}
		return payload, nil
	}

	if err := r.ParseForm(); err != nil {
		return payload, fmt.Errorf("invalid form payload: %w", err)
	}
	payload.Text = r.Form.Get("text")
	payload.Category = r.Form.Get("category")
	payload.Direction = r.Form.Get("direction")
	payload.Symbols = r.Form.Get("symbols")
	payload.Source = r.Form.Get("source")
	payload.Urgency = parseManualFloat(r.Form.Get("urgency"))
	payload.Strength = parseManualFloat(r.Form.Get("strength"))
	return payload, nil
}

func buildManualSignal(payload manualInputPayload, now time.Time) (signal.Signal, error) {
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		return signal.Signal{}, errors.New("text is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	category := strings.TrimSpace(strings.ToLower(payload.Category))
	if category == "" {
		category = "corporate"
	}
	source := strings.TrimSpace(strings.ToLower(payload.Source))
	if source == "" {
		source = "manual-input"
	}
	urgency := clampManualScore(payload.Urgency, 0.8)
	strength := clampManualScore(payload.Strength, urgency)

	raw, _ := json.Marshal(map[string]any{
		"text":      text,
		"category":  category,
		"direction": payload.Direction,
		"symbols":   payload.Symbols,
		"manual":    true,
	})

	return signal.Signal{
		ID:                    "manual-" + uuid.New().String(),
		Source:                source,
		Type:                  signal.TypeNews,
		Category:              category,
		Timestamp:             now,
		Urgency:               urgency,
		Strength:              strength,
		Direction:             parseManualDirection(payload.Direction),
		Entities:              manualEntities(payload.Symbols),
		Raw:                   raw,
		OriginalText:          text,
		Translated:            text,
		Languages:             []string{"en"},
		TranslationProvider:   "manual",
		TranslationConfidence: 1,
	}, nil
}

func manualEntities(raw string) []signal.Entity {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})
	seen := map[string]struct{}{}
	entities := make([]signal.Entity, 0, len(parts))
	for _, part := range parts {
		symbol := strings.ToUpper(strings.TrimSpace(part))
		if symbol == "" {
			continue
		}
		if _, ok := seen[symbol]; ok {
			continue
		}
		seen[symbol] = struct{}{}
		entities = append(entities, signal.Entity{Name: symbol, Type: "instrument", ID: symbol})
	}
	return entities
}

func parseManualDirection(raw string) signal.Direction {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "bullish", "long", "buy":
		return signal.Bullish
	case "bearish", "short", "sell":
		return signal.Bearish
	default:
		return signal.Neutral
	}
}

func parseManualFloat(raw string) float64 {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return value
}

func clampManualScore(value, fallback float64) float64 {
	if value <= 0 {
		value = fallback
	}
	if value < 0.01 {
		return 0.01
	}
	if value > 1 {
		return 1
	}
	return value
}

func manualInputPage(token string) string {
	hiddenToken := ""
	if strings.TrimSpace(token) != "" {
		hiddenToken = `<input type="hidden" name="token" value="` + html.EscapeString(token) + `">`
	}
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Trading Floor Manual Input</title>
  <style>
    :root { color-scheme: dark; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; min-height: 100vh; background: #0b0f14; color: #e7edf5; display: grid; place-items: center; }
    main { width: min(780px, calc(100vw - 32px)); }
    h1 { font-size: 22px; font-weight: 650; margin: 0 0 18px; letter-spacing: 0; }
    form { display: grid; gap: 12px; }
    label { display: grid; gap: 6px; color: #b7c2d0; font-size: 13px; }
    textarea, input, select { background: #111820; border: 1px solid #2d3a4a; border-radius: 6px; color: #f3f7fb; font: inherit; padding: 10px 12px; }
    textarea { min-height: 150px; resize: vertical; }
    .grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
    button { background: #e7edf5; border: 0; border-radius: 6px; color: #0b0f14; cursor: pointer; font: inherit; font-weight: 700; padding: 12px 14px; }
    button:hover { background: #ffffff; }
    @media (max-width: 720px) { .grid { grid-template-columns: 1fr 1fr; } }
  </style>
</head>
<body>
  <main>
    <h1>Manual Catalyst Input</h1>
    <form method="post" action="/manual-signal">
      ` + hiddenToken + `
      <label>
        Catalyst
        <textarea name="text" required placeholder="Ferrari launched an EV with weak claimed range relative to luxury EV peers; market may underprice brand and margin risk."></textarea>
      </label>
      <div class="grid">
        <label>
          Symbols
          <input name="symbols" value="RACE">
        </label>
        <label>
          Category
          <select name="category">
            <option value="corporate">corporate</option>
            <option value="sector">sector</option>
            <option value="macro">macro</option>
            <option value="geopolitical">geopolitical</option>
            <option value="flows">flows</option>
            <option value="volatility">volatility</option>
            <option value="prediction_market">prediction_market</option>
          </select>
        </label>
        <label>
          Direction
          <select name="direction">
            <option value="bearish">bearish</option>
            <option value="bullish">bullish</option>
            <option value="neutral">neutral</option>
          </select>
        </label>
        <label>
          Urgency
          <input name="urgency" value="0.8" inputmode="decimal">
        </label>
      </div>
      <button type="submit">Inject Catalyst</button>
    </form>
  </main>
</body>
</html>`
}
