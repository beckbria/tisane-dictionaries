package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tisane-wiki", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		inputFlag  string
		sourceURL  string
		outputPath string
		workDir    string
		dateFlag   string
		keepDump   bool
	)

	fs.StringVar(&inputFlag, "i", "", "Local already-downloaded dump file(s), comma-separated")
	fs.StringVar(&inputFlag, "input", "", "Local already-downloaded dump file(s), comma-separated")
	fs.StringVar(&sourceURL, "u", "", "Remote Wikimedia dump URL or index URL (default: "+DefaultPageTitlesURL+")")
	fs.StringVar(&sourceURL, "url", "", "Remote Wikimedia dump URL or index URL (default: "+DefaultPageTitlesURL+")")
	fs.StringVar(&outputPath, "o", "", "Output .txt file path (date and .txt extension are enforced)")
	fs.StringVar(&outputPath, "output", "", "Output .txt file path (date and .txt extension are enforced)")
	fs.StringVar(&workDir, "work-dir", "", "Directory for temporary downloaded dump files")
	fs.StringVar(&dateFlag, "date", "", "Override dump date (YYYYMMDD) for output filename")
	fs.BoolVar(&keepDump, "keep-dump", false, "Preserve downloaded dump file(s) after processing instead of deleting")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "Tisane Wikipedia Article Title Extractor\n\n")
		fmt.Fprintf(stderr, "Usage:\n")
		fmt.Fprintf(stderr, "  tisane-wiki [options] [local-dump-file...]\n\n")
		fmt.Fprintf(stderr, "Examples:\n")
		fmt.Fprintf(stderr, "  tisane-wiki                                  # Fetch latest enwiki pagetitles dump (prompts for confirmation)\n")
		fmt.Fprintf(stderr, "  tisane-wiki enwiki-20261116-all-titles-in-ns-0.gz\n")
		fmt.Fprintf(stderr, "  tisane-wiki -u https://dumps.wikimedia.org/other/pagetitles/\n\n")
		fmt.Fprintf(stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	var localFiles []string
	if inputFlag != "" {
		for _, part := range strings.Split(inputFlag, ",") {
			p := strings.TrimSpace(part)
			if p != "" {
				localFiles = append(localFiles, p)
			}
		}
	}
	for _, arg := range fs.Args() {
		if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
			if sourceURL != "" {
				fmt.Fprintln(stderr, "tisane-wiki: multiple URLs specified")
				return 2
			}
			sourceURL = arg
		} else {
			localFiles = append(localFiles, arg)
		}
	}

	if len(localFiles) > 0 && sourceURL != "" {
		fmt.Fprintln(stderr, "tisane-wiki: specify either local dump file(s) or a remote URL, not both")
		return 2
	}

	if len(localFiles) > 0 {
		return runLocal(localFiles, outputPath, dateFlag, stdout, stderr)
	}

	if sourceURL == "" {
		sourceURL = DefaultPageTitlesURL
	}
	return runRemote(sourceURL, outputPath, workDir, dateFlag, keepDump, stdin, stdout, stderr)
}

func runLocal(files []string, outputPath, dateOverride string, stdout, stderr io.Writer) int {
	date := strings.TrimSpace(dateOverride)
	prefix := ""
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			fmt.Fprintf(stderr, "tisane-wiki: cannot access %q: %v\n", f, err)
			return 1
		}
		if info.IsDir() {
			fmt.Fprintf(stderr, "tisane-wiki: %q is a directory, expected a dump file\n", f)
			return 1
		}
		if date == "" {
			date = ExtractDateFromStrings(filepath.Base(f))
		}
		if prefix == "" {
			prefix = extractWikiPrefix(filepath.Base(f))
		}
	}
	if date == "" {
		if info, err := os.Stat(files[0]); err == nil {
			date = info.ModTime().UTC().Format("20060102")
		} else {
			date = time.Now().UTC().Format("20060102")
		}
	}

	finalOut := EnsureOutputFilename(outputPath, prefix, date)
	outFile, bw, err := createOutputWriter(finalOut)
	if err != nil {
		fmt.Fprintf(stderr, "tisane-wiki: %v\n", err)
		return 1
	}

	var totalTitles int64
	for _, f := range files {
		fmt.Fprintf(stdout, "Extracting titles from %s...\n", f)
		n, err := ExtractTitlesFromFile(f, bw)
		if err != nil {
			outFile.Close()
			fmt.Fprintf(stderr, "tisane-wiki: %v\n", err)
			return 1
		}
		totalTitles += n
	}

	if err := bw.Flush(); err != nil {
		outFile.Close()
		fmt.Fprintf(stderr, "tisane-wiki: failed flushing output: %v\n", err)
		return 1
	}
	if err := outFile.Close(); err != nil {
		fmt.Fprintf(stderr, "tisane-wiki: failed closing output: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Wrote %d titles to %s\n", totalTitles, finalOut)
	return 0
}

func runRemote(
	sourceURL, outputPath, workDir, dateOverride string,
	keepDump bool,
	stdin io.Reader, stdout, stderr io.Writer,
) int {
	if IsHistoryDumpURL(sourceURL) {
		fmt.Fprintf(stderr, "tisane-wiki: refusing full-history dump %q (only latest revision dumps are allowed)\n", sourceURL)
		return 2
	}

	inReader := bufio.NewReader(stdin)
	ua, err := PromptUserAgent(inReader, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "tisane-wiki: %v\n", err)
		return 1
	}

	client := NewWikimediaClient(ua)
	fmt.Fprintf(stdout, "Checking %s...\n", sourceURL)
	plan, err := client.ResolveDumpURLs(sourceURL)
	if err != nil {
		fmt.Fprintf(stderr, "tisane-wiki: %v\n", err)
		return 1
	}

	if strings.TrimSpace(dateOverride) != "" {
		plan.Date = strings.TrimSpace(dateOverride)
	}
	finalOut := EnsureOutputFilename(outputPath, plan.WikiPrefix, plan.Date)

	confirmed, err := ConfirmDownload(inReader, stdout, plan, finalOut)
	if err != nil {
		fmt.Fprintf(stderr, "tisane-wiki: failed reading confirmation: %v\n", err)
		return 1
	}
	if !confirmed {
		fmt.Fprintln(stdout, "Download cancelled by user.")
		return 0
	}

	tempWorkDir := workDir
	createdTemp := false
	if tempWorkDir == "" {
		if keepDump {
			tempWorkDir = "downloads"
		} else {
			tempWorkDir, err = os.MkdirTemp("", "tisane-wiki-*")
			if err != nil {
				fmt.Fprintf(stderr, "tisane-wiki: failed creating temp directory: %v\n", err)
				return 1
			}
			createdTemp = true
		}
	}
	if createdTemp {
		defer os.RemoveAll(tempWorkDir)
	}

	outFile, bw, err := createOutputWriter(finalOut)
	if err != nil {
		fmt.Fprintf(stderr, "tisane-wiki: %v\n", err)
		return 1
	}

	totalTitles, pipeErr := RunDownloadPipeline(context.Background(), client, plan.URLs, tempWorkDir, bw, keepDump, stdout)
	flushErr := bw.Flush()
	closeErr := outFile.Close()

	if pipeErr != nil {
		fmt.Fprintf(stderr, "tisane-wiki: %v\n", pipeErr)
		return 1
	}
	if flushErr != nil {
		fmt.Fprintf(stderr, "tisane-wiki: failed flushing output: %v\n", flushErr)
		return 1
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "tisane-wiki: failed closing output: %v\n", closeErr)
		return 1
	}

	fmt.Fprintf(stdout, "Wrote %d titles to %s\n", totalTitles, finalOut)
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
