// Package websocket manages real-time bi-directional communication, client sessions, and messaging hub.
package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	htmlStd "html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

/*
LinkPreview represents extracted OpenGraph and HTML metadata for a URL link.
*/
type LinkPreview struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
}

/*
MessageUpdatedEvent is dispatched via WebSocket to both sender and online receiver when a message's metadata is updated.
*/
type MessageUpdatedEvent struct {
	Type      string `json:"type"`
	MessageID int    `json:"message_id"`
	Metadata  any    `json:"metadata"`
}

// LinkPreviewEvent is kept as an alias for backwards compatibility.
type LinkPreviewEvent = MessageUpdatedEvent

// urlRegex matches absolute URLs starting with http:// or https://.
var urlRegex = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"]+`)

/*
ExtractURL scans message text content for the first occurrence of an http:// or https:// URL.
It trims common trailing punctuation like commas, periods, or brackets.
*/
func ExtractURL(content string) string {
	match := urlRegex.FindString(content)
	if match == "" {
		return ""
	}

	// Trim trailing punctuation marks commonly found at the end of sentences
	cleaned := strings.TrimRight(match, ".,!?;:)\"'>]")
	parsed, err := url.ParseRequestURI(cleaned)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return cleaned
}

/*
FetchLinkPreview performs an HTTP GET request to targetURL, parses OpenGraph and HTML metadata,
and returns a structured LinkPreview entity.
*/
func FetchLinkPreview(ctx context.Context, targetURL string) (*LinkPreview, error) {
	if _, err := url.ParseRequestURI(targetURL); err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL content: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("target URL returned HTTP status %d", resp.StatusCode)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml+xml") {
		return nil, fmt.Errorf("non-HTML content type: %s", contentType)
	}

	// Limit response stream to 512KB to prevent memory exhaustion
	limitReader := io.LimitReader(resp.Body, 512*1024)
	return parseHTMLMeta(limitReader, resp.Request.URL)
}

// parseHTMLMeta tokenizes the HTML stream to extract title, description, image, and site_name.
func parseHTMLMeta(r io.Reader, baseURL *url.URL) (*LinkPreview, error) {
	z := html.NewTokenizer(r)

	var (
		rawTitle   string
		ogTitle    string
		rawDesc    string
		ogDesc     string
		ogImage    string
		ogSiteName string
		inTitleTag bool
	)

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}

		switch tt {
		case html.StartTagToken:
			t := z.Token()
			if strings.EqualFold(t.Data, "title") {
				inTitleTag = true
			} else if strings.EqualFold(t.Data, "meta") {
				extractMetaAttributes(t.Attr, &ogTitle, &rawDesc, &ogDesc, &ogImage, &ogSiteName)
			}

		case html.SelfClosingTagToken:
			t := z.Token()
			if strings.EqualFold(t.Data, "meta") {
				extractMetaAttributes(t.Attr, &ogTitle, &rawDesc, &ogDesc, &ogImage, &ogSiteName)
			}

		case html.EndTagToken:
			t := z.Token()
			if strings.EqualFold(t.Data, "title") {
				inTitleTag = false
			}

		case html.TextToken:
			if inTitleTag {
				rawTitle += z.Token().Data
			}
		}

		// Optimization: if all key OpenGraph fields are populated, stop parsing early
		if ogTitle != "" && ogDesc != "" && ogImage != "" && ogSiteName != "" {
			break
		}
	}

	finalTitle := htmlStd.UnescapeString(strings.TrimSpace(ogTitle))
	if finalTitle == "" {
		finalTitle = htmlStd.UnescapeString(strings.TrimSpace(rawTitle))
	}

	finalDesc := htmlStd.UnescapeString(strings.TrimSpace(ogDesc))
	if finalDesc == "" {
		finalDesc = htmlStd.UnescapeString(strings.TrimSpace(rawDesc))
	}

	finalSiteName := htmlStd.UnescapeString(strings.TrimSpace(ogSiteName))
	if finalSiteName == "" && baseURL != nil {
		finalSiteName = baseURL.Hostname()
	}

	finalImage := strings.TrimSpace(ogImage)
	if finalImage != "" && baseURL != nil {
		if parsedImg, err := url.Parse(finalImage); err == nil {
			finalImage = baseURL.ResolveReference(parsedImg).String()
		}
	}

	if finalTitle == "" && finalDesc == "" && finalImage == "" {
		return nil, fmt.Errorf("no title, description, or image metadata found in page")
	}

	return &LinkPreview{
		URL:         baseURL.String(),
		Title:       finalTitle,
		Description: finalDesc,
		Image:       finalImage,
		SiteName:    finalSiteName,
	}, nil
}

// extractMetaAttributes inspects HTML meta tag attributes for og:* and standard description tags.
func extractMetaAttributes(attrs []html.Attribute, ogTitle, rawDesc, ogDesc, ogImage, ogSiteName *string) {
	var prop, name, content string
	for _, attr := range attrs {
		switch strings.ToLower(attr.Key) {
		case "property":
			prop = strings.ToLower(attr.Val)
		case "name":
			name = strings.ToLower(attr.Val)
		case "content":
			content = attr.Val
		}
	}

	if content == "" {
		return
	}

	switch {
	case prop == "og:title" && *ogTitle == "":
		*ogTitle = content
	case (name == "title" || name == "twitter:title") && *ogTitle == "":
		*ogTitle = content
	case prop == "og:description" && *ogDesc == "":
		*ogDesc = content
	case (name == "description" || name == "twitter:description") && *rawDesc == "":
		*rawDesc = content
	case (prop == "og:image" || prop == "og:image:url" || prop == "og:image:secure_url" || name == "twitter:image") && *ogImage == "":
		*ogImage = content
	case prop == "og:site_name" && *ogSiteName == "":
		*ogSiteName = content
	}
}

/*
fetchAndUpdateLinkPreview runs asynchronously in a background goroutine:
1. Fetches link preview metadata from rawURL with a 5s timeout.
2. Checks if title was extracted.
3. Updates the messages table metadata column with {"url": "...", "title": "..."} JSONB.
4. Relays {"type": "message_updated", "message_id": ..., "metadata": ...} via WebSocket to the sender and (if online) the receiver.
*/
func (h *Hub) fetchAndUpdateLinkPreview(messageID, senderID, receiverID int, rawURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	preview, err := FetchLinkPreview(ctx, rawURL)
	if err != nil {
		log.Printf("[LinkPreview] Skipping preview for message %d (URL: %s): %v", messageID, rawURL, err)
		return
	}

	if preview.Title == "" {
		log.Printf("[LinkPreview] No title found for message %d (URL: %s)", messageID, rawURL)
		return
	}

	metadataObj := map[string]any{
		"url":   preview.URL,
		"title": preview.Title,
	}
	if preview.Description != "" {
		metadataObj["description"] = preview.Description
	}
	if preview.Image != "" {
		metadataObj["image"] = preview.Image
	}
	if preview.SiteName != "" {
		metadataObj["site_name"] = preview.SiteName
	}

	metadataJSON, err := json.Marshal(metadataObj)
	if err != nil {
		log.Printf("[LinkPreview] Failed to marshal metadata for message %d: %v", messageID, err)
		return
	}

	updateQuery := `
		UPDATE messages 
		SET metadata = $1::jsonb 
		WHERE id = $2;
	`
	if _, err := h.DB.Exec(ctx, updateQuery, metadataJSON, messageID); err != nil {
		log.Printf("[LinkPreview] Failed to update message %d metadata in DB: %v", messageID, err)
		return
	}

	event := MessageUpdatedEvent{
		Type:      "message_updated",
		MessageID: messageID,
		Metadata:  metadataObj,
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		log.Printf("[LinkPreview] Failed to marshal message_updated event for message %d: %v", messageID, err)
		return
	}

	// Dispatch update to sender
	h.SendToUser(senderID, eventJSON)

	// Dispatch update to receiver if currently online
	if h.IsUserOnline(receiverID) {
		h.SendToUser(receiverID, eventJSON)
	}
}

// ProcessLinkPreview is an exported alias for fetchAndUpdateLinkPreview.
func (h *Hub) ProcessLinkPreview(messageID, senderID, receiverID int, rawURL string) {
	h.fetchAndUpdateLinkPreview(messageID, senderID, receiverID, rawURL)
}

// fetchAndUpdateLinkPreview is a package-level helper that dispatches to Hub.fetchAndUpdateLinkPreview.
func fetchAndUpdateLinkPreview(h *Hub, messageID, senderID, receiverID int, rawURL string) {
	h.fetchAndUpdateLinkPreview(messageID, senderID, receiverID, rawURL)
}
