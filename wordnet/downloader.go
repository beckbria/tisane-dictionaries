package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const DefaultDownloadsURL = "https://en-word.net/downloads"

var (
	hrefRe        = regexp.MustCompile(`(?i)href\s*=\s*"([^"]+)"`)
	wordnetDumpRe = regexp.MustCompile(`(?i)^(?:english-wordnet|oewn)-([0-9]{4})(-plus)?\.xml(\.gz)?$`)
)

// DumpCandidate represents a discovered Open English Wordnet XML dump link.
type DumpCandidate struct {
	URL      string
	Filename string
	Year     int
	IsPlus   bool
	IsGzip   bool
}

// WordnetClient fetches the downloads index and dump files.
type WordnetClient struct {
	HTTP *http.Client
}

func NewWordnetClient() *WordnetClient {
	return &WordnetClient{
		HTTP: &http.Client{},
	}
}

func (c *WordnetClient) get(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TisaneDictionary/0.1 (+https://github.com/beckbria/tisane-dictionaries)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rawURL)
	}
	return resp, nil
}

// ResolveDumpURL resolves either a direct XML/XML.GZ dump URL or a downloads page URL
// (such as https://en-word.net/downloads) to the best dump candidate.
func (c *WordnetClient) ResolveDumpURL(sourceURL string) (*DumpCandidate, error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("invalid source URL %q: %w", sourceURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme must be http or https: %q", sourceURL)
	}

	baseName := path.Base(u.Path)
	if looksLikeXMLDump(baseName) {
		cand, ok := parseDumpCandidate(sourceURL, baseName)
		if !ok {
			cand = DumpCandidate{
				URL:      sourceURL,
				Filename: baseName,
				IsGzip:   strings.HasSuffix(strings.ToLower(baseName), ".gz"),
			}
		}
		return &cand, nil
	}

	links, finalURL, err := c.fetchPageLinks(sourceURL)
	if err != nil {
		return nil, err
	}

	return SelectLatestWordnetDump(finalURL, links)
}

func (c *WordnetClient) fetchPageLinks(pageURL string) ([]string, string, error) {
	resp, err := c.get(pageURL)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch %s: %w", pageURL, err)
	}
	defer resp.Body.Close()

	finalURL := pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, "", fmt.Errorf("failed to read %s: %w", pageURL, err)
	}

	matches := hrefRe.FindAllStringSubmatch(string(body), -1)
	links := make([]string, 0, len(matches))
	for _, m := range matches {
		href := strings.TrimSpace(m[1])
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "?") {
			continue
		}
		links = append(links, href)
	}
	return links, finalURL, nil
}

// SelectLatestWordnetDump selects the latest Open English Wordnet XML dump from a list of hrefs.
// It prefers higher year first, then a "plus" edition over a standard edition, then .xml.gz over .xml.
func SelectLatestWordnetDump(baseURL string, links []string) (*DumpCandidate, error) {
	baseParsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL %q: %w", baseURL, err)
	}

	var candidates []DumpCandidate
	seen := make(map[string]bool)

	for _, href := range links {
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		resolved := baseParsed.ResolveReference(ref)
		fullURL := resolved.String()
		if seen[fullURL] {
			continue
		}
		fileName := path.Base(resolved.Path)
		if cand, ok := parseDumpCandidate(fullURL, fileName); ok {
			seen[fullURL] = true
			candidates = append(candidates, cand)
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no Open English Wordnet XML dump links found at %s", baseURL)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Year != b.Year {
			return a.Year > b.Year
		}
		if a.IsPlus != b.IsPlus {
			return a.IsPlus && !b.IsPlus
		}
		if a.IsGzip != b.IsGzip {
			return a.IsGzip && !b.IsGzip
		}
		return a.Filename < b.Filename
	})

	best := candidates[0]
	return &best, nil
}

func parseDumpCandidate(fullURL, fileName string) (DumpCandidate, bool) {
	m := wordnetDumpRe.FindStringSubmatch(fileName)
	if m == nil {
		return DumpCandidate{}, false
	}
	year, err := strconv.Atoi(m[1])
	if err != nil {
		return DumpCandidate{}, false
	}
	isPlus := strings.EqualFold(m[2], "-plus")
	isGzip := strings.EqualFold(m[3], ".gz")
	return DumpCandidate{
		URL:      fullURL,
		Filename: fileName,
		Year:     year,
		IsPlus:   isPlus,
		IsGzip:   isGzip,
	}, true
}

func looksLikeXMLDump(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".xml.gz") || strings.HasSuffix(lower, ".xml")
}

// DownloadFile downloads rawURL into destPath via a temporary .part file.
func (c *WordnetClient) DownloadFile(rawURL, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	partPath := destPath + ".part"
	resp, err := c.get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(partPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(partPath)
		return fmt.Errorf("download failed for %s: %w", rawURL, copyErr)
	}
	if closeErr != nil {
		os.Remove(partPath)
		return fmt.Errorf("failed closing %s: %w", partPath, closeErr)
	}
	if err := os.Rename(partPath, destPath); err != nil {
		os.Remove(partPath)
		return err
	}
	return nil
}
