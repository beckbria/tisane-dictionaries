package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const DefaultPageTitlesURL = "https://dumps.wikimedia.org/other/pagetitles/"

var (
	dateDirRe         = regexp.MustCompile(`^([0-9]{8})/?$`)
	dateInNameRe      = regexp.MustCompile(`([0-9]{8})`)
	enwikiPageTitleRe = regexp.MustCompile(`^enwiki-[0-9]+-all-titles-in-ns[^/]*$`)
	anyPageTitleRe    = regexp.MustCompile(`^[a-z0-9_-]+-[0-9]+-all-titles-in-ns[^/]*$`)
	pagesArticlesRe   = regexp.MustCompile(`^[a-z0-9_-]+-(?:[0-9]+|latest)-pages-articles[^/]*\.xml[^/]*$`)
	hrefRe            = regexp.MustCompile(`(?i)href\s*=\s*"([^"]+)"`)
	wikiPrefixRe      = regexp.MustCompile(`^([a-z0-9_]+wiki[a-z]*)-`)
)

// ValidateURL checks that raw is a valid http or https URL with a non-empty host.
func ValidateURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("URL cannot be empty")
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return "", fmt.Errorf("URL cannot contain whitespace")
	}
	u, err := url.ParseRequestURI(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("URL scheme must be http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	host := u.Hostname()
	if host == "" || (!strings.Contains(host, ".") && host != "localhost") {
		return "", fmt.Errorf("URL host %q is not a valid domain", host)
	}
	return trimmed, nil
}

// ValidateEmail checks that raw is a valid bare email address.
func ValidateEmail(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("email cannot be empty")
	}
	if strings.ContainsAny(trimmed, " \t\r\n<>") {
		return "", fmt.Errorf("email must be a bare address without spaces or angle brackets")
	}
	addr, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid email address: %w", err)
	}
	if addr.Address != trimmed {
		return "", fmt.Errorf("email must be a bare address (e.g. user@example.com)")
	}
	at := strings.LastIndex(addr.Address, "@")
	if at <= 0 || !strings.Contains(addr.Address[at+1:], ".") {
		return "", fmt.Errorf("email domain must contain a '.'")
	}
	return addr.Address, nil
}

// FormatUserAgent builds a Wikimedia-compliant User-Agent string.
func FormatUserAgent(contactURL, email string) string {
	return fmt.Sprintf("TisaneDictionary/0.0 (%s; %s)", contactURL, email)
}

// PromptUserAgent prompts the user for a contact URL and email address,
// validates both, and returns the formatted User-Agent header value.
func PromptUserAgent(r *bufio.Reader, out io.Writer) (string, error) {
	fmt.Fprintln(out, "Wikimedia User-Agent Policy requires a contact URL and email address.")

	var contactURL string
	for {
		fmt.Fprint(out, "Enter your contact URL (e.g. https://example.com): ")
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", fmt.Errorf("failed to read URL: %w", err)
		}
		v, valErr := ValidateURL(line)
		if valErr == nil {
			contactURL = v
			break
		}
		fmt.Fprintf(out, "Invalid URL: %v\n", valErr)
		if err != nil {
			return "", fmt.Errorf("failed to read URL: %w", err)
		}
	}

	var email string
	for {
		fmt.Fprint(out, "Enter your contact email (e.g. you@example.com): ")
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", fmt.Errorf("failed to read email: %w", err)
		}
		v, valErr := ValidateEmail(line)
		if valErr == nil {
			email = v
			break
		}
		fmt.Fprintf(out, "Invalid email: %v\n", valErr)
		if err != nil {
			return "", fmt.Errorf("failed to read email: %w", err)
		}
	}

	return FormatUserAgent(contactURL, email), nil
}

// DumpPlan describes resolved remote dump files ready for confirmation and download.
type DumpPlan struct {
	URLs       []string
	Date       string
	WikiPrefix string
}

// WikimediaClient performs single-stream HTTP requests with the required User-Agent.
type WikimediaClient struct {
	HTTP      *http.Client
	UserAgent string
}

func NewWikimediaClient(userAgent string) *WikimediaClient {
	return &WikimediaClient{
		HTTP: &http.Client{
			Timeout: 0, // large dump streams may take a while; rely on transport timeouts
		},
		UserAgent: userAgent,
	}
}

func (c *WikimediaClient) get(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
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

// IsHistoryDumpURL reports whether a URL or filename refers to a full-history dump.
func IsHistoryDumpURL(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	return strings.Contains(lower, "meta-history") || strings.Contains(lower, "pages-meta-history") || strings.Contains(lower, "stub-meta-history")
}

// ResolveDumpURLs resolves a base or direct dump URL into a DumpPlan.
func (c *WikimediaClient) ResolveDumpURLs(sourceURL string) (*DumpPlan, error) {
	if IsHistoryDumpURL(sourceURL) {
		return nil, fmt.Errorf("refusing full-history dump %q: only latest-revision dumps are permitted", sourceURL)
	}

	u, err := url.Parse(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("invalid source URL %q: %w", sourceURL, err)
	}

	isDefaultSource := strings.HasPrefix(sourceURL, DefaultPageTitlesURL)

	// If URL does not end with '/', check whether it is a direct file or an HTML directory.
	if !strings.HasSuffix(u.Path, "/") && looksLikeDumpFile(path.Base(u.Path)) {
		date := ExtractDateFromStrings(u.Path)
		if date == "" {
			date = time.Now().UTC().Format("20060102")
		}
		return &DumpPlan{
			URLs:       []string{sourceURL},
			Date:       date,
			WikiPrefix: extractWikiPrefix(path.Base(u.Path)),
		}, nil
	}

	// Ensure directory URL ends with '/' for clean relative link resolution.
	dirURL := sourceURL
	if !strings.HasSuffix(dirURL, "/") {
		dirURL += "/"
	}

	links, err := c.fetchDirLinks(dirURL)
	if err != nil {
		return nil, err
	}

	// Check if this directory has YYYYMMDD/ subdirectories and no direct dump files.
	directFiles := filterDumpFiles(links, isDefaultSource)
	if len(directFiles) == 0 {
		var dateDirs []string
		for _, link := range links {
			clean := strings.TrimPrefix(link, "./")
			if m := dateDirRe.FindStringSubmatch(clean); m != nil {
				dateDirs = append(dateDirs, m[1])
			}
		}
		if len(dateDirs) > 0 {
			sort.Strings(dateDirs)
			latestDate := dateDirs[len(dateDirs)-1]
			subURL, err := resolveRelativeURL(dirURL, latestDate+"/")
			if err != nil {
				return nil, err
			}
			subLinks, err := c.fetchDirLinks(subURL)
			if err != nil {
				return nil, err
			}
			subFiles := filterDumpFiles(subLinks, isDefaultSource)
			if len(subFiles) == 0 {
				return nil, fmt.Errorf("no matching dump files found in %s", subURL)
			}
			return buildDumpPlan(subURL, subFiles, latestDate)
		}
		return nil, fmt.Errorf("no matching dump files found at %s", dirURL)
	}

	date := ExtractDateFromStrings(dirURL)
	return buildDumpPlan(dirURL, directFiles, date)
}

func (c *WikimediaClient) fetchDirLinks(dirURL string) ([]string, error) {
	resp, err := c.get(dirURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch index %s: %w", dirURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read index %s: %w", dirURL, err)
	}

	matches := hrefRe.FindAllStringSubmatch(string(body), -1)
	links := make([]string, 0, len(matches))
	for _, m := range matches {
		href := strings.TrimSpace(m[1])
		if href == "" || href == "../" || strings.HasPrefix(href, "?") || strings.HasPrefix(href, "#") {
			continue
		}
		links = append(links, href)
	}
	return links, nil
}

func filterDumpFiles(links []string, preferEnwiki bool) []string {
	var enwiki []string
	var anyPageTitle []string
	var articlesXML []string

	for _, link := range links {
		base := path.Base(strings.TrimSuffix(link, "/"))
		if base == "" || strings.HasSuffix(link, "/") {
			continue
		}
		if IsHistoryDumpURL(base) || isAuxiliaryFile(base) {
			continue
		}
		if enwikiPageTitleRe.MatchString(base) {
			enwiki = append(enwiki, link)
		} else if anyPageTitleRe.MatchString(base) {
			anyPageTitle = append(anyPageTitle, link)
		} else if pagesArticlesRe.MatchString(base) && !strings.Contains(strings.ToLower(base), "multistream-index") {
			articlesXML = append(articlesXML, link)
		}
	}

	if len(enwiki) > 0 {
		sort.Strings(enwiki)
		return enwiki
	}
	if !preferEnwiki {
		if len(anyPageTitle) > 0 {
			sort.Strings(anyPageTitle)
			return anyPageTitle
		}
		if len(articlesXML) > 0 {
			sort.Strings(articlesXML)
			return articlesXML
		}
	}
	return nil
}

func buildDumpPlan(baseURL string, files []string, fallbackDate string) (*DumpPlan, error) {
	urls := make([]string, 0, len(files))
	date := fallbackDate
	prefix := ""
	for _, f := range files {
		resolved, err := resolveRelativeURL(baseURL, f)
		if err != nil {
			return nil, err
		}
		urls = append(urls, resolved)
		if date == "" {
			date = ExtractDateFromStrings(path.Base(resolved))
		}
		if prefix == "" {
			prefix = extractWikiPrefix(path.Base(resolved))
		}
	}
	if date == "" {
		date = time.Now().UTC().Format("20060102")
	}
	if prefix == "" {
		prefix = "wikipedia"
	}
	return &DumpPlan{
		URLs:       urls,
		Date:       date,
		WikiPrefix: prefix,
	}, nil
}

func resolveRelativeURL(baseStr, refStr string) (string, error) {
	base, err := url.Parse(baseStr)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(refStr)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func isAuxiliaryFile(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".md5", ".sha1", ".sha256", ".txt", ".html", ".json", ".rss", ".torrent", ".sig"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func looksLikeDumpFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".gz") ||
		strings.HasSuffix(lower, ".bz2") ||
		strings.HasSuffix(lower, ".xml") ||
		strings.Contains(lower, "all-titles-in-ns")
}

// ExtractDateFromStrings returns the first 8-digit YYYYMMDD date found in the inputs.
func ExtractDateFromStrings(inputs ...string) string {
	for _, s := range inputs {
		if m := dateInNameRe.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

func extractWikiPrefix(name string) string {
	if m := wikiPrefixRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return "wikipedia"
}

// ConfirmDownload displays the download plan and requires explicit 'y' or 'yes' confirmation.
func ConfirmDownload(r *bufio.Reader, out io.Writer, plan *DumpPlan, outputPath string) (bool, error) {
	fmt.Fprintln(out, "\nDownload plan:")
	fmt.Fprintf(out, "  Dump date:   %s\n", plan.Date)
	fmt.Fprintf(out, "  Output file: %s\n", outputPath)
	fmt.Fprintf(out, "  Files (%d):\n", len(plan.URLs))
	for _, u := range plan.URLs {
		fmt.Fprintf(out, "    - %s\n", u)
	}
	fmt.Fprint(out, "Proceed with download? [y/N]: ")

	line, err := r.ReadString('\n')
	if err != nil && len(line) == 0 {
		return false, err
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
}

// DownloadFile downloads rawURL as a single stream into destPath (via a .part temp file).
func (c *WikimediaClient) DownloadFile(rawURL, destPath string) error {
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
