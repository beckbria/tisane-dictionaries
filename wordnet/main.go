package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tisane-wordnet", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		inputPath  string
		sourceURL  string
		outputPath string
		workDir    string
		keepDump   bool
	)

	fs.StringVar(&inputPath, "i", "", "Local already-downloaded WordNet LMF XML dump file (.xml.gz or .xml)")
	fs.StringVar(&inputPath, "input", "", "Local already-downloaded WordNet LMF XML dump file (.xml.gz or .xml)")
	fs.StringVar(&sourceURL, "u", "", "Remote WordNet downloads page or direct dump URL (default: "+DefaultDownloadsURL+")")
	fs.StringVar(&sourceURL, "url", "", "Remote WordNet downloads page or direct dump URL (default: "+DefaultDownloadsURL+")")
	fs.StringVar(&outputPath, "o", "", "Output .txt file path (defaults to <dump-basename>.txt)")
	fs.StringVar(&outputPath, "output", "", "Output .txt file path (defaults to <dump-basename>.txt)")
	fs.StringVar(&workDir, "work-dir", "", "Directory for downloaded dump file")
	fs.BoolVar(&keepDump, "keep-dump", false, "Preserve downloaded dump file after processing instead of deleting")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "Tisane Open English WordNet Extractor\n\n")
		fmt.Fprintf(stderr, "Usage:\n")
		fmt.Fprintf(stderr, "  tisane-wordnet [options] [local-dump-file | remote-url]\n\n")
		fmt.Fprintf(stderr, "Examples:\n")
		fmt.Fprintf(stderr, "  tisane-wordnet                                       # Download latest Open English WordNet (preferring plus) and extract words\n")
		fmt.Fprintf(stderr, "  tisane-wordnet english-wordnet-2025-plus.xml.gz      # Extract words from a local dump into english-wordnet-2025-plus.txt\n")
		fmt.Fprintf(stderr, "  tisane-wordnet -u https://en-word.net/downloads\n\n")
		fmt.Fprintf(stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	for _, arg := range fs.Args() {
		if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
			if sourceURL != "" {
				fmt.Fprintln(stderr, "tisane-wordnet: multiple URLs specified")
				return 2
			}
			sourceURL = arg
		} else {
			if inputPath != "" {
				fmt.Fprintln(stderr, "tisane-wordnet: multiple input files specified")
				return 2
			}
			inputPath = arg
		}
	}

	if inputPath != "" && sourceURL != "" {
		fmt.Fprintln(stderr, "tisane-wordnet: specify either a local dump file or a remote URL, not both")
		return 2
	}

	if inputPath != "" {
		return runLocal(inputPath, outputPath, stdout, stderr)
	}

	if sourceURL == "" {
		sourceURL = DefaultDownloadsURL
	}
	return runRemote(sourceURL, outputPath, workDir, keepDump, stdout, stderr)
}

func runLocal(inputPath, outputPath string, stdout, stderr io.Writer) int {
	info, err := os.Stat(inputPath)
	if err != nil {
		fmt.Fprintf(stderr, "tisane-wordnet: cannot access %q: %v\n", inputPath, err)
		return 1
	}
	if info.IsDir() {
		fmt.Fprintf(stderr, "tisane-wordnet: %q is a directory, expected a dump file\n", inputPath)
		return 1
	}

	finalOut := DeriveOutputFilename(inputPath, outputPath)
	return extractToFile(inputPath, finalOut, stdout, stderr)
}

func runRemote(sourceURL, outputPath, workDir string, keepDump bool, stdout, stderr io.Writer) int {
	client := NewWordnetClient()
	fmt.Fprintf(stdout, "Checking %s for latest Open English WordNet dump...\n", sourceURL)
	cand, err := client.ResolveDumpURL(sourceURL)
	if err != nil {
		fmt.Fprintf(stderr, "tisane-wordnet: %v\n", err)
		return 1
	}

	finalOut := DeriveOutputFilename(cand.Filename, outputPath)

	dlDir := workDir
	createdTemp := false
	if dlDir == "" {
		if keepDump {
			dlDir = "."
		} else {
			dlDir, err = os.MkdirTemp("", "tisane-wordnet-*")
			if err != nil {
				fmt.Fprintf(stderr, "tisane-wordnet: failed creating temp directory: %v\n", err)
				return 1
			}
			createdTemp = true
		}
	}
	if createdTemp {
		defer os.RemoveAll(dlDir)
	}

	destPath := filepath.Join(dlDir, cand.Filename)
	fmt.Fprintf(stdout, "Downloading %s...\n", cand.URL)
	if err := client.DownloadFile(cand.URL, destPath); err != nil {
		fmt.Fprintf(stderr, "tisane-wordnet: %v\n", err)
		return 1
	}
	if !keepDump && !createdTemp {
		defer os.Remove(destPath)
	}

	return extractToFile(destPath, finalOut, stdout, stderr)
}

func extractToFile(inputPath, finalOut string, stdout, stderr io.Writer) int {
	outFile, bw, err := createOutputWriter(finalOut)
	if err != nil {
		fmt.Fprintf(stderr, "tisane-wordnet: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Extracting words from %s...\n", inputPath)
	totalWords, err := ExtractWordsFromFile(inputPath, bw)
	if err != nil {
		outFile.Close()
		fmt.Fprintf(stderr, "tisane-wordnet: %v\n", err)
		return 1
	}

	if err := bw.Flush(); err != nil {
		outFile.Close()
		fmt.Fprintf(stderr, "tisane-wordnet: failed flushing output: %v\n", err)
		return 1
	}
	if err := outFile.Close(); err != nil {
		fmt.Fprintf(stderr, "tisane-wordnet: failed closing output: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Wrote %d words to %s\n", totalWords, finalOut)
	return 0
}

func createOutputWriter(outputPath string) (*os.File, *bufio.Writer, error) {
	if dir := filepath.Dir(outputPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("failed creating output directory %q: %w", dir, err)
		}
	}
	f, err := os.Create(outputPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating output file %q: %w", outputPath, err)
	}
	return f, bufio.NewWriterSize(f, 256*1024), nil
}
