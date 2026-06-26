package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestBuildManualSignalCreatesTradeableCatalystSignal(t *testing.T) {
	sig, err := buildManualSignal(manualInputPayload{
		Text:      "Ferrari launches EV with weak range versus luxury peers.",
		Category:  "corporate",
		Direction: "bearish",
		Symbols:   "RACE, MU",
		Urgency:   0.9,
	}, time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildManualSignal returned error: %v", err)
	}
	if sig.Source != "manual-input" || sig.Type != signal.TypeNews {
		t.Fatalf("unexpected source/type: %s %s", sig.Source, sig.Type)
	}
	if sig.Category != "corporate" || sig.Direction != signal.Bearish {
		t.Fatalf("unexpected category/direction: %s %s", sig.Category, sig.Direction)
	}
	if len(sig.Entities) != 2 || sig.Entities[0].Name != "RACE" || sig.Entities[1].Name != "MU" {
		t.Fatalf("unexpected entities: %+v", sig.Entities)
	}
	if sig.Translated == "" || sig.TranslationProvider != "manual" {
		t.Fatalf("manual text was not preserved: %+v", sig)
	}
}

func TestManualInputHandlerPublishesSignal(t *testing.T) {
	var published signal.Signal
	handler := newManualInputHandler(func(_ context.Context, sig signal.Signal) error {
		published = sig
		return nil
	}, "", nil)

	req := httptest.NewRequest(http.MethodPost, "/manual-signal", strings.NewReader(
		"text=Ferrari+launches+weak-range+EV&symbols=RACE&category=corporate&direction=bearish&urgency=0.8",
	))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if published.ID == "" || published.Entities[0].Name != "RACE" || published.Direction != signal.Bearish {
		t.Fatalf("unexpected published signal: %+v", published)
	}
}
