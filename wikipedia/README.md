# `tisane-wiki` — Wikipedia Article Title Extractor

Extracts Wikipedia article titles (one title per line) from Wikimedia page-title dumps (`all-titles-in-ns-0.gz`) or MediaWiki XML dumps for input into `tisane-build`.

## Build

```bash
go build -o tisane-wiki .
```

## Usage

### 1. Download and extract the latest English Wikipedia page-titles dump

```bash
./tisane-wiki
```

- Prompts for a contact URL and email address (validated) to construct the Wikimedia-compliant `User-Agent`: `TisaneDictionary/0.0 (<url>; <email>)`.
- Resolves the latest `enwiki-<YYYYMMDD>-all-titles-in-ns*` dump from `https://dumps.wikimedia.org/other/pagetitles/`.
- Displays the download plan and requires explicit `y`/`yes` confirmation before downloading.
- Downloads using a single stream and keeps at most two shard files on disk at any time (deleting each shard after processing unless `--keep-dump` is passed).
- Writes `<prefix>-<YYYYMMDD>-titles.txt`.

### 2. Extract from an already-downloaded dump

```bash
./tisane-wiki enwiki-20261116-all-titles-in-ns-0.gz
```

### 3. Build a Tisane `.tsd` dictionary

```bash
/home/brian/tea/tisane-build enwiki-20261116-titles.txt enwiki-20261116.tsd
```

### Options

- `-i, --input`: Local already-downloaded dump file(s) (comma-separated).
- `-u, --url`: Remote Wikimedia dump URL or directory index URL.
- `-o, --output`: Output `.txt` file path (dump date and `.txt` extension are enforced).
- `--date`: Override dump date (`YYYYMMDD`) for output filename.
- `--work-dir`: Directory for temporary downloaded shards.
- `--keep-dump`: Preserve downloaded dump file(s) after processing instead of deleting.
