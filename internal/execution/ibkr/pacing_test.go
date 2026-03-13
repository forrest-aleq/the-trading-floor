package ibkr

import (
	"context"
	"testing"
	"time"
)

func TestPacingBudgetAcquireMessage(t *testing.T) {
	p := NewPacingBudget()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go p.Run(ctx)

	// Should be able to acquire at least one token immediately
	if err := p.AcquireMessage(ctx); err != nil {
		t.Fatalf("AcquireMessage failed: %v", err)
	}

	stats := p.Stats()
	if stats.MsgTokensAvailable != 49 {
		t.Fatalf("expected 49 tokens remaining, got %d", stats.MsgTokensAvailable)
	}
}

func TestPacingBudgetMarketDataLines(t *testing.T) {
	p := NewPacingBudget()

	// Acquire up to max
	for i := 0; i < 15; i++ {
		if !p.AcquireMarketDataLine() {
			t.Fatalf("should be able to acquire line %d", i+1)
		}
	}

	// 16th should fail
	if p.AcquireMarketDataLine() {
		t.Fatal("should not be able to exceed max lines")
	}

	// Release one and try again
	p.ReleaseMarketDataLine()
	if !p.AcquireMarketDataLine() {
		t.Fatal("should be able to acquire after release")
	}
}

func TestPacingBudgetQualifyCache(t *testing.T) {
	p := NewPacingBudget()

	// First time: should qualify
	if !p.ShouldQualify("AAPL") {
		t.Fatal("expected ShouldQualify=true for new symbol")
	}

	// Record and check again
	p.RecordQualify("AAPL")
	if p.ShouldQualify("AAPL") {
		t.Fatal("expected ShouldQualify=false for recently qualified symbol")
	}

	// Different symbol should still need qualification
	if !p.ShouldQualify("MSFT") {
		t.Fatal("expected ShouldQualify=true for different symbol")
	}
}
