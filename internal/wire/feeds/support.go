package feeds

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultFeedSeenLimit = 2048
	maxFeedBodyBytes     = 2 << 20
	maxFeedBackoff       = 30 * time.Minute
)

func newFeedHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          64,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

func newFeedRequest(ctx context.Context, method, sourceURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, sourceURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", feedUserAgent())
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, application/json;q=0.8, */*;q=0.5")
	if strings.Contains(strings.ToLower(req.URL.Host), "sec.gov") {
		if contact := strings.TrimSpace(os.Getenv("WIRE_CONTACT_EMAIL")); contact != "" {
			req.Header.Set("From", contact)
		}
	}
	return req, nil
}

func feedUserAgent() string {
	if value := strings.TrimSpace(os.Getenv("WIRE_USER_AGENT")); value != "" {
		return value
	}
	if contact := strings.TrimSpace(os.Getenv("WIRE_CONTACT_EMAIL")); contact != "" {
		return "TradingFloor/1.0 (" + contact + ")"
	}
	return "TradingFloor/1.0"
}

type sourceState struct {
	mu sync.Mutex

	seen       map[string]struct{}
	seenOrder  []string
	maxSeen    int
	failures   int
	suppressed time.Time
}

func newSourceState(maxSeen int) *sourceState {
	if maxSeen <= 0 {
		maxSeen = defaultFeedSeenLimit
	}
	return &sourceState{
		seen:    make(map[string]struct{}, maxSeen),
		maxSeen: maxSeen,
	}
}

func (s *sourceState) Seen(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.seen[id]; ok {
		return true
	}

	s.seen[id] = struct{}{}
	s.seenOrder = append(s.seenOrder, id)
	if len(s.seenOrder) <= s.maxSeen {
		return false
	}

	oldest := s.seenOrder[0]
	s.seenOrder = append([]string(nil), s.seenOrder[1:]...)
	delete(s.seen, oldest)
	return false
}

func (s *sourceState) ShouldPoll(now time.Time) (bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.suppressed.IsZero() || !now.Before(s.suppressed) {
		return false, 0
	}
	return true, s.suppressed.Sub(now)
}

func (s *sourceState) RecordFailure(now time.Time, base time.Duration) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	if base <= 0 {
		base = time.Minute
	}
	if base < 30*time.Second {
		base = 30 * time.Second
	}

	s.failures++
	backoff := base
	for i := 1; i < s.failures; i++ {
		backoff *= 2
		if backoff >= maxFeedBackoff {
			backoff = maxFeedBackoff
			break
		}
	}
	s.suppressed = now.Add(backoff)
	return backoff
}

func (s *sourceState) RecordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failures = 0
	s.suppressed = time.Time{}
}

func signalTimestamp(raw string) time.Time {
	if ts, ok := parsePublishedTime(raw); ok {
		return ts.UTC()
	}
	return time.Now().UTC()
}

func parsePublishedTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 02 Jan 2006 15:04:05 MST",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05-0700",
		time.DateOnly,
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts, true
		}
	}

	return time.Time{}, false
}

func readFeedInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func readFeedDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
