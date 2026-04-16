package model

import (
	"encoding/json"
	"strings"
	"time"
)

type ColleagueMessageKind string

const (
	ColleagueMessageProposal ColleagueMessageKind = "proposal"
	ColleagueMessageReply    ColleagueMessageKind = "reply"
)

// ColleagueMessage is the structured payload desks use when sharing theses
// internally. It keeps collaboration replayable and graph-persistable without
// relying on prompt-only behavior.
type ColleagueMessage struct {
	ThreadID         string               `json:"thread_id,omitempty"`
	MessageID        string               `json:"message_id,omitempty"`
	ReplyToMessageID string               `json:"reply_to_message_id,omitempty"`
	OriginDesk       string               `json:"origin_desk,omitempty"`
	OriginDomain     string               `json:"origin_domain,omitempty"`
	OriginSignalID   string               `json:"origin_signal_id,omitempty"`
	ThesisID         string               `json:"thesis_id,omitempty"`
	ParentThesisID   string               `json:"parent_thesis_id,omitempty"`
	RootThesisID     string               `json:"root_thesis_id,omitempty"`
	TargetDomains    []string             `json:"target_domains,omitempty"`
	Structure        string               `json:"structure,omitempty"`
	Strategy         string               `json:"strategy,omitempty"`
	Conviction       float64              `json:"conviction,omitempty"`
	InternalDepth    int                  `json:"internal_depth,omitempty"`
	Kind             ColleagueMessageKind `json:"kind,omitempty"`
	RequestedAction  string               `json:"requested_action,omitempty"`
	Subject          string               `json:"subject,omitempty"`
	Summary          string               `json:"summary,omitempty"`
	DisplaySymbol    string               `json:"display_symbol,omitempty"`
}

type CollaborationInput struct {
	ThreadID               string               `json:"thread_id,omitempty"`
	MessageID              string               `json:"message_id,omitempty"`
	OriginDesk             string               `json:"origin_desk,omitempty"`
	OriginDomain           string               `json:"origin_domain,omitempty"`
	OriginSignalID         string               `json:"origin_signal_id,omitempty"`
	OriginThesisID         string               `json:"origin_thesis_id,omitempty"`
	RootThesisID           string               `json:"root_thesis_id,omitempty"`
	Kind                   ColleagueMessageKind `json:"kind,omitempty"`
	RequestedAction        string               `json:"requested_action,omitempty"`
	Summary                string               `json:"summary,omitempty"`
	RelationshipTrust      float64              `json:"relationship_trust,omitempty"`
	RelationshipConfidence float64              `json:"relationship_confidence,omitempty"`
	RelationshipHealth     float64              `json:"relationship_health,omitempty"`
	RecoveryScore          float64              `json:"recovery_score,omitempty"`
	AppraisalClass         string               `json:"appraisal_class,omitempty"`
	FaceThreatScore        float64              `json:"face_threat_score,omitempty"`
	SocialCost             float64              `json:"social_cost,omitempty"`
}

type DeskRelationshipBelief struct {
	Key                string    `json:"key"`
	OriginDesk         string    `json:"origin_desk"`
	ReceivingDesk      string    `json:"receiving_desk"`
	Domain             string    `json:"domain"`
	Regime             string    `json:"regime"`
	Trust              float64   `json:"trust"`
	Confidence         float64   `json:"confidence"`
	RelationshipHealth float64   `json:"relationship_health"`
	RecoveryScore      float64   `json:"recovery_score"`
	PositiveRecoveries int       `json:"positive_recoveries"`
	NegativeViolations int       `json:"negative_violations"`
	SuccessCount       int       `json:"success_count"`
	FailureCount       int       `json:"failure_count"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func DecodeColleagueMessage(raw json.RawMessage) (ColleagueMessage, bool) {
	if len(raw) == 0 {
		return ColleagueMessage{}, false
	}
	var message ColleagueMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return ColleagueMessage{}, false
	}
	message = message.Normalize()
	if message.IsZero() {
		return ColleagueMessage{}, false
	}
	return message, true
}

func (m ColleagueMessage) Encode() json.RawMessage {
	normalized := m.Normalize()
	raw, _ := json.Marshal(normalized)
	return raw
}

func (m ColleagueMessage) Normalize() ColleagueMessage {
	m.ThreadID = strings.TrimSpace(m.ThreadID)
	m.MessageID = strings.TrimSpace(m.MessageID)
	m.ReplyToMessageID = strings.TrimSpace(m.ReplyToMessageID)
	m.OriginDesk = strings.TrimSpace(m.OriginDesk)
	m.OriginDomain = normalizeCollabLower(m.OriginDomain)
	m.OriginSignalID = strings.TrimSpace(m.OriginSignalID)
	m.ThesisID = strings.TrimSpace(m.ThesisID)
	m.ParentThesisID = strings.TrimSpace(m.ParentThesisID)
	m.RootThesisID = strings.TrimSpace(m.RootThesisID)
	m.TargetDomains = normalizeCollabDomains(m.TargetDomains)
	m.Structure = strings.TrimSpace(m.Structure)
	m.Strategy = strings.TrimSpace(m.Strategy)
	m.RequestedAction = strings.TrimSpace(m.RequestedAction)
	m.Subject = strings.TrimSpace(m.Subject)
	m.Summary = strings.TrimSpace(m.Summary)
	m.DisplaySymbol = strings.TrimSpace(m.DisplaySymbol)
	if m.InternalDepth < 0 {
		m.InternalDepth = 0
	}
	if m.Kind == "" {
		if m.ReplyToMessageID != "" {
			m.Kind = ColleagueMessageReply
		} else {
			m.Kind = ColleagueMessageProposal
		}
	}
	if m.RootThesisID == "" {
		m.RootThesisID = firstNonEmptyCollabString(m.ParentThesisID, m.ThesisID)
	}
	return m
}

func (m ColleagueMessage) IsZero() bool {
	return m.ThreadID == "" && m.MessageID == "" && m.OriginDesk == "" && m.ThesisID == "" && len(m.TargetDomains) == 0
}

func NewColleagueThreadID(thesisID string) string {
	thesisID = strings.TrimSpace(thesisID)
	if thesisID == "" {
		return ""
	}
	return "thread-" + thesisID
}

func NewColleagueMessageID(thesisID string) string {
	thesisID = strings.TrimSpace(thesisID)
	if thesisID == "" {
		return ""
	}
	return "msg-" + thesisID
}

func CollaborationInputFromMessage(message ColleagueMessage) *CollaborationInput {
	message = message.Normalize()
	if message.IsZero() {
		return nil
	}
	return &CollaborationInput{
		ThreadID:        message.ThreadID,
		MessageID:       message.MessageID,
		OriginDesk:      message.OriginDesk,
		OriginDomain:    message.OriginDomain,
		OriginSignalID:  message.OriginSignalID,
		OriginThesisID:  firstNonEmptyCollabString(message.ThesisID, message.ParentThesisID),
		RootThesisID:    message.RootThesisID,
		Kind:            message.Kind,
		RequestedAction: message.RequestedAction,
		Summary:         message.Summary,
	}
}

func normalizeCollabDomains(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeCollabLower(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func normalizeCollabLower(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonEmptyCollabString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
