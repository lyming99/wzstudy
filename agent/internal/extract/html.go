package extract

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

type Page struct {
	Title       string
	Description string
	Text        string
}

var skipped = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true,
	"iframe": true, "template": true, "form": true, "nav": true, "footer": true,
}

var blocks = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "section": true,
	"article": true, "header": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "blockquote": true, "pre": true, "table": true,
}

// HTML extracts a readable title, meta description and body text from raw HTML.
// It is deliberately heuristic: the agent only needs compact evidence, not a
// pixel-perfect rendering.
func HTML(raw []byte) Page {
	document, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return Page{}
	}
	var title, description, text strings.Builder
	var walk func(node *html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			name := strings.ToLower(node.Data)
			if skipped[name] {
				return
			}
			switch name {
			case "title":
				if title.Len() == 0 {
					title.WriteString(nodeText(node))
				}
			case "meta":
				if strings.EqualFold(metaAttribute(node, "name"), "description") && description.Len() == 0 {
					description.WriteString(metaAttribute(node, "content"))
				}
			case "h1":
				if title.Len() == 0 {
					title.WriteString(nodeText(node))
				}
			}
			if blocks[name] {
				text.WriteString("\n")
			}
		}
		if node.Type == html.TextNode {
			chunk := strings.Join(strings.Fields(node.Data), " ")
			if chunk != "" {
				text.WriteString(chunk)
				text.WriteString(" ")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == html.ElementNode && blocks[strings.ToLower(node.Data)] {
			text.WriteString("\n")
		}
	}
	walk(document)
	return Page{
		Title:       compact(title.String(), 500),
		Description: compact(description.String(), 2000),
		Text:        normalizeText(text.String()),
	}
}

func nodeText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && skipped[strings.ToLower(current.Data)] {
			return
		}
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteString(" ")
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return compact(builder.String(), 1000)
}

func metaAttribute(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}
	return ""
}

func compact(value string, maximum int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= maximum {
		return value
	}
	cut := value[:maximum]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut)
}

func normalizeText(value string) string {
	lines := strings.Split(value, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
