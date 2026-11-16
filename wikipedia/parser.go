package main

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

// ExtractTitlesFromFile reads a Wikipedia dump file (plain, .gz, or .bz2;
// pagetitles text or MediaWiki XML) and writes one article title per line to w.
// Returns the number of titles written.
func ExtractTitlesFromFile(path string, w *bufio.Writer) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64*1024)
	magic, _ := br.Peek(3)

	var reader io.Reader = br
	var closer io.Closer

	switch {
	case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		gz, err := gzip.NewReader(br)
		if err != nil {
			return 0, fmt.Errorf("failed to open gzip stream %s: %w", path, err)
		}
		closer = gz
		reader = gz
	case len(magic) >= 3 && magic[0] == 'B' && magic[1] == 'Z' && magic[2] == 'h':
		reader = bzip2.NewReader(br)
	default:
		lower := strings.ToLower(path)
		if strings.HasSuffix(lower, ".gz") {
			gz, err := gzip.NewReader(br)
			if err != nil {
				return 0, fmt.Errorf("failed to open gzip stream %s: %w", path, err)
			}
			closer = gz
			reader = gz
		} else if strings.HasSuffix(lower, ".bz2") {
			reader = bzip2.NewReader(br)
		}
	}

	if closer != nil {
		defer closer.Close()
	}

	contentReader := bufio.NewReaderSize(reader, 256*1024)
	isXML, err := looksLikeXML(contentReader)
	if err != nil && err != io.EOF {
		return 0, fmt.Errorf("failed reading %s: %w", path, err)
	}
	if err == io.EOF {
		return 0, nil
	}

	if isXML {
		return extractFromMediaWikiXML(contentReader, w)
	}
	return extractFromPageTitles(contentReader, w)
}

func looksLikeXML(br *bufio.Reader) (bool, error) {
	peek, err := br.Peek(512)
	if len(peek) == 0 && err != nil {
		return false, err
	}
	trimmed := bytes.TrimSpace(peek)
	return len(trimmed) > 0 && trimmed[0] == '<', nil
}

func extractFromPageTitles(br *bufio.Reader, w *bufio.Writer) (int64, error) {
	var count int64
	firstLine := true

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if firstLine {
				firstLine = false
				if trimmed == "page_title" {
					if err == io.EOF {
						break
					}
					continue
				}
			}
			if trimmed != "" {
				title := strings.ReplaceAll(trimmed, "_", " ")
				if _, wErr := w.WriteString(title); wErr != nil {
					return count, wErr
				}
				if wErr := w.WriteByte('\n'); wErr != nil {
					return count, wErr
				}
				count++
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

func extractFromMediaWikiXML(r io.Reader, w *bufio.Writer) (int64, error) {
	dec := xml.NewDecoder(r)
	var (
		count   int64
		inPage  bool
		title   string
		ns      string
		curElem string
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, fmt.Errorf("XML parse error: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "page":
				inPage = true
				title = ""
				ns = ""
			case "title", "ns":
				if inPage {
					curElem = t.Name.Local
				}
			}
		case xml.CharData:
			if inPage {
				switch curElem {
				case "title":
					title += string(t)
				case "ns":
					ns += string(t)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "title", "ns":
				curElem = ""
			case "page":
				if inPage && strings.TrimSpace(ns) == "0" {
					cleanTitle := strings.TrimSpace(title)
					if cleanTitle != "" {
						if _, wErr := w.WriteString(cleanTitle); wErr != nil {
							return count, wErr
						}
						if wErr := w.WriteByte('\n'); wErr != nil {
							return count, wErr
						}
						count++
					}
				}
				inPage = false
			}
		}
	}
	return count, nil
}
