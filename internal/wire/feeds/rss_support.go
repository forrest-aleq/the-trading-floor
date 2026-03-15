package feeds

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
)

type feedItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
}

func fetchFeedItems(ctx context.Context, client *http.Client, sourceURL string) ([]feedItem, error) {
	req, err := newFeedRequest(ctx, http.MethodGet, sourceURL)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBodyBytes))
	if err != nil {
		return nil, err
	}

	// RSS 2.0
	var rss struct {
		Channel struct {
			Items []feedItem `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		return rss.Channel.Items, nil
	}

	// Atom
	var atom struct {
		Entries []struct {
			Title string `xml:"title"`
			Link  struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
			Summary   string `xml:"summary"`
			Content   string `xml:"content"`
			ID        string `xml:"id"`
			Updated   string `xml:"updated"`
			Published string `xml:"published"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		items := make([]feedItem, len(atom.Entries))
		for i, entry := range atom.Entries {
			description := entry.Summary
			if description == "" {
				description = entry.Content
			}
			published := entry.Updated
			if published == "" {
				published = entry.Published
			}
			items[i] = feedItem{
				Title:       entry.Title,
				Link:        entry.Link.Href,
				Description: description,
				GUID:        entry.ID,
				PubDate:     published,
			}
		}
		return items, nil
	}

	return nil, fmt.Errorf("could not parse RSS or Atom feed")
}
