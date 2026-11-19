package websocket

import (
	"net/url"
	"strings"
	"testing"
)

func TestExtractURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple https URL",
			input:    "Check out https://github.com/golang/go for Go source code",
			expected: "https://github.com/golang/go",
		},
		{
			name:     "Simple http URL",
			input:    "Visit http://example.com today",
			expected: "http://example.com",
		},
		{
			name:     "URL with trailing punctuation",
			input:    "Have you seen https://google.com?!",
			expected: "https://google.com",
		},
		{
			name:     "URL in parenthesis",
			input:    "Link: (https://openai.com)",
			expected: "https://openai.com",
		},
		{
			name:     "No URL present",
			input:    "Hello world, how are you?",
			expected: "",
		},
		{
			name:     "Invalid scheme ftp",
			input:    "ftp://ftp.is.co.za/rfc/rfc1808.txt",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractURL(tt.input)
			if result != tt.expected {
				t.Errorf("ExtractURL(%q) = %q; want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseHTMLMeta(t *testing.T) {
	htmlContent := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Fallback Title</title>
		<meta property="og:title" content="OpenGraph Title" />
		<meta property="og:description" content="This is an OpenGraph description." />
		<meta property="og:image" content="/assets/cover.png" />
		<meta property="og:site_name" content="MyWebsite" />
	</head>
	<body>
		<p>Some page body</p>
	</body>
	</html>
	`

	baseURL, err := url.Parse("https://mywebsite.com/posts/hello")
	if err != nil {
		t.Fatalf("Failed to parse base URL: %v", err)
	}

	preview, err := parseHTMLMeta(strings.NewReader(htmlContent), baseURL)
	if err != nil {
		t.Fatalf("parseHTMLMeta failed: %v", err)
	}

	if preview.Title != "OpenGraph Title" {
		t.Errorf("expected Title %q, got %q", "OpenGraph Title", preview.Title)
	}
	if preview.Description != "This is an OpenGraph description." {
		t.Errorf("expected Description %q, got %q", "This is an OpenGraph description.", preview.Description)
	}
	expectedImage := "https://mywebsite.com/assets/cover.png"
	if preview.Image != expectedImage {
		t.Errorf("expected Image %q, got %q", expectedImage, preview.Image)
	}
	if preview.SiteName != "MyWebsite" {
		t.Errorf("expected SiteName %q, got %q", "MyWebsite", preview.SiteName)
	}
}

func TestParseHTMLTitleOnly(t *testing.T) {
	htmlContent := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Simple Page &amp; Guide</title>
	</head>
	<body>
		<p>No meta tags here</p>
	</body>
	</html>
	`

	baseURL, err := url.Parse("https://example.com/guide")
	if err != nil {
		t.Fatalf("Failed to parse base URL: %v", err)
	}

	preview, err := parseHTMLMeta(strings.NewReader(htmlContent), baseURL)
	if err != nil {
		t.Fatalf("parseHTMLMeta failed: %v", err)
	}

	if preview.Title != "Simple Page & Guide" {
		t.Errorf("expected Title %q, got %q", "Simple Page & Guide", preview.Title)
	}
	if preview.SiteName != "example.com" {
		t.Errorf("expected SiteName %q, got %q", "example.com", preview.SiteName)
	}
}
