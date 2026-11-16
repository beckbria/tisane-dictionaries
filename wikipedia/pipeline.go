package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// EnsureOutputFilename guarantees the output path includes the dump date and ends with ".txt".
func EnsureOutputFilename(requested, wikiPrefix, date string) string {
	if wikiPrefix == "" {
		wikiPrefix = "wikipedia"
	}
	if date == "" {
		date = "unknown"
	}
	if strings.TrimSpace(requested) == "" {
		return fmt.Sprintf("%s-%s-titles.txt", wikiPrefix, date)
	}

	dir := filepath.Dir(requested)
	base := filepath.Base(requested)

	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if ext == "" || !strings.EqualFold(ext, ".txt") {
		if ext != "" {
			stem = base
		}
		ext = ".txt"
	}
	if !strings.Contains(stem, date) {
		stem = fmt.Sprintf("%s-%s", stem, date)
	}
	return filepath.Join(dir, stem+ext)
}

// RunDownloadPipeline downloads dump URLs one stream at a time and processes them
// into outWriter. At most two downloaded/downloading files exist in workDir at any
// time: one being processed and one being downloaded (or waiting for processing).
// After each file is processed, it is deleted unless keepFiles is true.
func RunDownloadPipeline(
	ctx context.Context,
	client *WikimediaClient,
	urls []string,
	workDir string,
	outWriter *bufio.Writer,
	keepFiles bool,
	logOut io.Writer,
) (int64, error) {
	return runDownloadPipelineWithHook(ctx, client, urls, workDir, outWriter, keepFiles, logOut, nil)
}

type downloadedShard struct {
	index int
	path  string
	err   error
}

func runDownloadPipelineWithHook(
	ctx context.Context,
	client *WikimediaClient,
	urls []string,
	workDir string,
	outWriter *bufio.Writer,
	keepFiles bool,
	logOut io.Writer,
	onProcessStart func(shardPath string),
) (int64, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return 0, err
	}

	// Capacity 2: at most two files on disk at any time (1 being processed, 1 downloading/ready).
	slots := make(chan struct{}, 2)
	readyCh := make(chan downloadedShard, 2)

	pipeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Single downloader goroutine: strictly sequential single download stream.
	go func() {
		defer close(readyCh)
		for i, rawURL := range urls {
			select {
			case <-pipeCtx.Done():
				return
			case slots <- struct{}{}:
			}

			if pipeCtx.Err() != nil {
				<-slots
				return
			}

			fileName := shardFileName(i, rawURL)
			destPath := filepath.Join(workDir, fileName)
			if logOut != nil {
				fmt.Fprintf(logOut, "Downloading [%d/%d]: %s\n", i+1, len(urls), rawURL)
			}
			if err := client.DownloadFile(rawURL, destPath); err != nil {
				<-slots
				readyCh <- downloadedShard{index: i, err: err}
				return
			}
			readyCh <- downloadedShard{index: i, path: destPath}
		}
	}()

	var totalTitles int64
	for shard := range readyCh {
		if shard.err != nil {
			cancel()
			return totalTitles, shard.err
		}

		if onProcessStart != nil {
			onProcessStart(shard.path)
		}

		if logOut != nil {
			fmt.Fprintf(logOut, "Processing  [%d/%d]: %s\n", shard.index+1, len(urls), filepath.Base(shard.path))
		}
		n, err := ExtractTitlesFromFile(shard.path, outWriter)

		if !keepFiles {
			if rmErr := os.Remove(shard.path); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
				err = fmt.Errorf("failed deleting processed shard %s: %w", shard.path, rmErr)
			}
		}

		// Release slot only AFTER the file has been processed and deleted.
		<-slots

		if err != nil {
			cancel()
			// Drain remaining readyCh items and clean up if needed.
			for rem := range readyCh {
				if rem.path != "" && !keepFiles {
					_ = os.Remove(rem.path)
				}
				<-slots
			}
			return totalTitles, err
		}
		totalTitles += n
	}

	return totalTitles, nil
}

func shardFileName(index int, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil {
		base := path.Base(u.Path)
		if base != "" && base != "/" && base != "." {
			return fmt.Sprintf("%04d-%s", index, base)
		}
	}
	return fmt.Sprintf("shard-%04d.dump", index)
}
