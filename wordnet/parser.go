package main

import (
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DeriveOutputFilename derives the output .txt filename from the input dump filename
// (e.g., "english-wordnet-2025-plus.xml.gz" -> "english-wordnet-2025-plus.txt").
// If requestedOutput is non-empty, it ensures the path ends with ".txt".
func DeriveOutputFilename(inputName, requestedOutput string) string {
	if trimmed := strings.TrimSpace(requestedOutput); trimmed != "" {
		if strings.EqualFold(filepath.Ext(trimmed), ".txt") {
			return trimmed
		}
		return trimmed + ".txt"
	}

	base := filepath.Base(strings.TrimSpace(inputName))
	for _, suffix := range []string{".gz", ".bz2", ".xz", ".xml", ".zip"} {
		if len(base) > len(suffix) && strings.EqualFold(base[len(base)-len(suffix):], suffix) {
			base = base[:len(base)-len(suffix)]
		}
	}
	if base == "" || base == "." || base == "/" {
		base = "english-wordnet"
	}
	return base + ".txt"
}

// ExtractWordsFromFile reads a WordNet LMF XML dump (gzipped or plain XML)
// and writes each unique word (from <Lemma writtenForm="..."> and <Form writtenForm="...">)
// to w, one word per line. Returns the number of unique words written.
func ExtractWordsFromFile(path string, w *bufio.Writer) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64*1024)
	magic, _ := br.Peek(2)

	var reader io.Reader = br
	if (len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b) || strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return 0, fmt.Errorf("failed to open gzip stream %s: %w", path, err)
		}
		defer gz.Close()
		reader = gz
	}

	return ExtractWordsFromLMFXML(bufio.NewReaderSize(reader, 256*1024), w)
}

// ExtractWordsFromLMFXML streams a WordNet LMF XML document and writes all unique
// writtenForm values from <Lemma> and <Form> elements inside <LexicalEntry>, one per line.
func ExtractWordsFromLMFXML(r io.Reader, w *bufio.Writer) (int64, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false

	seen := make(map[string]struct{}, 163840)
	var (
		count          int64
		inLexicalEntry bool
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
			case "LexicalEntry":
				inLexicalEntry = true
			case "Lemma", "Form":
				if !inLexicalEntry {
					continue
				}
				word := getAttr(t.Attr, "writtenForm")
				word = strings.TrimSpace(word)
				if word == "" {
					continue
				}
				if _, exists := seen[word]; exists {
					continue
				}
				seen[word] = struct{}{}
				if _, wErr := w.WriteString(word); wErr != nil {
					return count, wErr
				}
				if wErr := w.WriteByte('\n'); wErr != nil {
					return count, wErr
				}
				count++
			}
		case xml.EndElement:
			if t.Name.Local == "LexicalEntry" {
				inLexicalEntry = false
			}
		}
	}

	return count, nil
}

func getAttr(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
