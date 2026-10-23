package feed

import (
	"bytes"
	"encoding/xml"
	"errors"
	"html"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"wzstudy/internal/study"
)

type rssItem struct {
	GUID        string   `xml:"guid"`
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	Encoded     string   `xml:"encoded"`
	Author      string   `xml:"author"`
	Creator     string   `xml:"creator"`
	Date        string   `xml:"pubDate"`
	Updated     string   `xml:"updated"`
	Tags        []string `xml:"category"`
}
type atomText struct {
	Text  string `xml:",chardata"`
	Inner string `xml:",innerxml"`
}

func (v atomText) value() string {
	if strings.TrimSpace(v.Text) != "" {
		return v.Text
	}
	return v.Inner
}

type atomEntry struct {
	ID        string   `xml:"id"`
	Title     atomText `xml:"title"`
	Summary   atomText `xml:"summary"`
	Content   atomText `xml:"content"`
	Published string   `xml:"published"`
	Updated   string   `xml:"updated"`
	Author    struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Tags []struct {
		Term string `xml:"term,attr"`
	} `xml:"category"`
}

func Parse(data []byte, base *url.URL) ([]study.Content, error) {
	if len(data) > MaxFeedBytes || !utf8.Valid(data) {
		return nil, errors.New("invalid_feed_size_or_encoding")
	}
	policy := bluemonday.StrictPolicy()
	clean := func(s string) string { return strings.TrimSpace(html.UnescapeString(policy.Sanitize(s))) }
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var root xml.StartElement
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, errors.New("invalid_feed_xml")
		}
		if _, ok := token.(xml.Directive); ok {
			return nil, errors.New("feed_directive_not_allowed")
		}
		if start, ok := token.(xml.StartElement); ok {
			root = start
			break
		}
	}
	out := []study.Content{}
	switch root.Name.Local {
	case "rss":
		var doc struct {
			Items []rssItem `xml:"channel>item"`
		}
		if err := decoder.DecodeElement(&doc, &root); err != nil {
			return nil, errors.New("invalid_rss")
		}
		for _, v := range doc.Items {
			if len(out) == 100 {
				break
			}
			link := resolve(base, strings.TrimSpace(v.Link))
			id := strings.TrimSpace(v.GUID)
			if id == "" {
				id = link
			}
			content := v.Encoded
			if content == "" {
				content = v.Description
			}
			author := v.Author
			if author == "" {
				author = v.Creator
			}
			out = append(out, study.Content{ExternalID: id, Title: clean(v.Title), Summary: clip(clean(v.Description), 8000), Content: clean(content), Format: "text", URL: link, Author: clean(author), Tags: cleanTags(v.Tags, clean), PublishedAt: parseDate(v.Date), SourceUpdatedAt: parseDate(v.Updated)})
		}
	case "feed":
		var doc struct {
			Entries []atomEntry `xml:"entry"`
		}
		if err := decoder.DecodeElement(&doc, &root); err != nil {
			return nil, errors.New("invalid_atom")
		}
		for _, v := range doc.Entries {
			if len(out) == 100 {
				break
			}
			link := ""
			for _, l := range v.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = resolve(base, l.Href)
					break
				}
			}
			id := strings.TrimSpace(v.ID)
			if id == "" {
				id = link
			}
			content := v.Content.value()
			if content == "" {
				content = v.Summary.value()
			}
			tags := []string{}
			for _, t := range v.Tags {
				tags = append(tags, t.Term)
			}
			out = append(out, study.Content{ExternalID: id, Title: clean(v.Title.value()), Summary: clip(clean(v.Summary.value()), 8000), Content: clean(content), Format: "text", URL: link, Author: clean(v.Author.Name), Tags: cleanTags(tags, clean), PublishedAt: parseDate(v.Published), SourceUpdatedAt: parseDate(v.Updated)})
		}
	default:
		return nil, errors.New("unsupported_feed_format")
	}
	// A feed without stable identities cannot be retried safely. Reject, don't
	// generate IDs from changing titles or the current poll timestamp.
	seen := map[string]bool{}
	for _, v := range out {
		if v.ExternalID == "" || v.Title == "" || len(v.Content) > 262144 || len(v.ExternalID) > 512 || seen[v.ExternalID] {
			return nil, errors.New("invalid_feed_item")
		}
		seen[v.ExternalID] = true
	}
	return out, nil
}
func resolve(base *url.URL, raw string) string {
	if raw == "" {
		return ""
	}
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	u.Fragment = ""
	if !study.ValidURL(u.String()) {
		return ""
	}
	return u.String()
}
func cleanTags(tags []string, clean func(string) string) []string {
	out := []string{}
	for _, v := range tags {
		v = clean(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func parseDate(s string) *time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC850, "Mon, 2 Jan 2006 15:04:05 -0700", "2006-01-02T15:04:05Z0700"} {
		if t, e := time.Parse(layout, strings.TrimSpace(s)); e == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}
