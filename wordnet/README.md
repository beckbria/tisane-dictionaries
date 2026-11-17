# `tisane-wordnet` — Open English WordNet Word Extractor

Extracts all words (`<Lemma writtenForm="...">` and `<Form writtenForm="...">`, one word per line) from Open English WordNet LMF XML dumps for input into `tisane-build`.

## Build

```bash
go build -o tisane-wordnet .
```

## Usage

### 1. Download and extract the latest Open English WordNet dump

```bash
./tisane-wordnet
```

- Checks `https://en-word.net/downloads` for the latest Open English WordNet LMF XML dump, preferring a `plus` edition (e.g., `english-wordnet-2025-plus.xml.gz`) over a standard edition (e.g., `english-wordnet-2025.xml.gz`).
- Downloads the `.xml.gz` dump to a temporary directory (deleted after processing unless `--keep-dump` is specified).
- Extracts all unique words and writes them to `<dump-name>.txt` (e.g., `english-wordnet-2025-plus.txt`).

### 2. Extract from an already-downloaded local dump

```bash
./tisane-wordnet ../english-wordnet-2025-plus.xml.gz
```

Writes `english-wordnet-2025-plus.txt`.

### 3. Build a Tisane `.tsd` dictionary

```bash
tisane-build english-wordnet-2025-plus.txt english-wordnet-2025-plus.tsd
```

### Options

- `-i, --input`: Local already-downloaded WordNet LMF XML dump file (`.xml.gz` or `.xml`).
- `-u, --url`: Remote WordNet downloads page or direct dump URL (default: `https://en-word.net/downloads`).
- `-o, --output`: Output `.txt` file path (defaults to `<dump-basename>.txt`).
- `--work-dir`: Directory for downloaded dump file.
- `--keep-dump`: Preserve downloaded dump file after processing instead of deleting.
