package firm

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

func TestBuildInternalSignalStartsConversationThread(t *testing.T) {
	thesis := &model.Thesis{
		ID:         "thesis-root",
		Domain:     "geopolitical",
		Strategy:   "event",
		Structure:  "single",
		Direction:  model.Long,
		Conviction: 0.84,
		Instrument: model.Instrument{Symbol: "XLE", SecType: "STK", Currency: "USD"},
	}
	origin := signal.Signal{ID: "sig-root", Source: "telegram/mena"}

	internal, ok := buildInternalSignal(origin, thesis, "desk-geo-a")
	if !ok {
		t.Fatal("expected internal signal to be published")
	}
	message, ok := model.DecodeColleagueMessage(internal.Raw)
	if !ok {
		t.Fatal("expected structured colleague payload")
	}
	if message.Kind != model.ColleagueMessageProposal {
		t.Fatalf("expected proposal kind, got %s", message.Kind)
	}
	if message.ThreadID != model.NewColleagueThreadID(thesis.ID) {
		t.Fatalf("unexpected thread id: %s", message.ThreadID)
	}
	if message.MessageID != model.NewColleagueMessageID(thesis.ID) {
		t.Fatalf("unexpected message id: %s", message.MessageID)
	}
	if message.ReplyToMessageID != "" {
		t.Fatalf("did not expect reply_to_message_id, got %s", message.ReplyToMessageID)
	}
	if len(message.TargetDomains) == 0 {
		t.Fatal("expected target domains on proposal")
	}
	if message.RootThesisID != thesis.ID {
		t.Fatalf("unexpected root thesis id: %s", message.RootThesisID)
	}
	if message.InternalDepth != 1 {
		t.Fatalf("expected depth 1, got %d", message.InternalDepth)
	}
}

func TestBuildInternalSignalRepliesIntoExistingConversationThread(t *testing.T) {
	parent := model.ColleagueMessage{
		ThreadID:        "thread-thesis-root",
		MessageID:       "msg-thesis-root",
		OriginDesk:      "desk-geo-a",
		OriginDomain:    "geopolitical",
		ThesisID:        "thesis-root",
		RootThesisID:    "thesis-root",
		TargetDomains:   []string{"macro", "tail"},
		InternalDepth:   1,
		Kind:            model.ColleagueMessageProposal,
		RequestedAction: "review",
	}
	origin := signal.Signal{ID: "sig-internal-root", Source: "internal/desk-geo-a", Raw: parent.Encode()}
	thesis := &model.Thesis{
		ID:         "thesis-macro",
		Domain:     "macro",
		Strategy:   "event",
		Structure:  "single",
		Direction:  model.Long,
		Conviction: 0.81,
		Instrument: model.Instrument{Symbol: "TLT", SecType: "STK", Currency: "USD"},
	}

	internal, ok := buildInternalSignal(origin, thesis, "desk-macro-a")
	if !ok {
		t.Fatal("expected colleague reply signal to be published")
	}
	message, ok := model.DecodeColleagueMessage(internal.Raw)
	if !ok {
		t.Fatal("expected structured colleague reply payload")
	}
	if message.Kind != model.ColleagueMessageReply {
		t.Fatalf("expected reply kind, got %s", message.Kind)
	}
	if message.ThreadID != parent.ThreadID {
		t.Fatalf("expected reply to stay in thread %s, got %s", parent.ThreadID, message.ThreadID)
	}
	if message.ReplyToMessageID != parent.MessageID {
		t.Fatalf("expected reply_to_message_id %s, got %s", parent.MessageID, message.ReplyToMessageID)
	}
	if message.ParentThesisID != parent.ThesisID {
		t.Fatalf("expected parent thesis id %s, got %s", parent.ThesisID, message.ParentThesisID)
	}
	if message.RootThesisID != parent.RootThesisID {
		t.Fatalf("expected root thesis id %s, got %s", parent.RootThesisID, message.RootThesisID)
	}
	if len(message.TargetDomains) != 1 || message.TargetDomains[0] != "geopolitical" {
		t.Fatalf("expected reply to route back to geopolitical, got %#v", message.TargetDomains)
	}
	if message.InternalDepth != 2 {
		t.Fatalf("expected depth 2, got %d", message.InternalDepth)
	}
}
