# goreenc - Go Video Reencoder

A robust, Go-based video encoder that re-encodes videos to HEVC (H.265) while preserving all streams and metadata. Only replaces files when encoding saves space.

## Features

- **Conservative encoding**: Only replaces files if the new file is smaller
- **Stream preservation**: Copies all audio, subtitles, and attachments exactly as-is
- **Smart profiles**: Automatic quality settings based on resolution
- **Automatic downscaling**: Videos >1080p are downscaled to 1080p (preserves aspect ratio)
- **Safe by default**: Dry-run mode prevents accidental file modification
- **Comprehensive logging**: Detailed before/after stats for every file
- **CSV export**: Track all encoding results in a CSV file
- **Resume support**: Skip files that are already HEVC encoded
- **Smart metadata tracking**: Remembers which files have been processed to avoid re-work
  - Tracks successful encodes
  - Tracks discarded encodes (where new file was larger)
  - Tracks failed encodes (separate flag to retry)

## Installation

### Build from source

```bash
# Using make (recommended)
make build

# Or directly with go
go build -o goreenc ./cmd/goreenc
```

The binary will be created in the current directory.

### Install to system

```bash
# Install to /usr/local/bin
make install

# Uninstall
make uninstall
```

### Makefile targets

```bash
make build              # Build optimized binary (2.2MB)
make build-debug        # Build with debug symbols
make build-linux        # Build for Linux (amd64)
make build-linux-arm64  # Build for Linux (arm64)
make build-windows      # Build for Windows (amd64)
make build-macos        # Build for macOS (amd64 + arm64)
make build-all          # Build for all platforms
make clean              # Remove build artifacts
make install            # Install to /usr/local/bin
make test               # Run tests
make fmt                # Format code
make vet                # Run go vet
make check              # Run fmt, vet, and test
make help               # Show all targets
```

### Dependencies

- `ffmpeg` - For video encoding
- `ffprobe` - For metadata extraction (usually included with ffmpeg)
- `file` - For MIME type detection (standard on most Unix systems)

## Usage

### Basic usage

```bash
# Dry-run to see what would happen (default, safe mode)
./goreenc /path/to/videos

# Actually encode videos
./goreenc --wetrun /path/to/videos

# Encode and replace originals (only if smaller)
./goreenc --wetrun --delete /path/to/videos
```

### Advanced options

```bash
# Set minimum file size threshold (in MB)
./goreenc --wetrun --size-threshold 1024 /path/to/videos

# Re-encode files already in HEVC
./goreenc --wetrun --override /path/to/videos

# Export stats to CSV
./goreenc --wetrun --stats-csv results.csv /path/to/videos

# Verbose logging
./goreenc --wetrun --verbose /path/to/videos

# Log to file (appends)
./goreenc --wetrun --log /var/log/goreenc.log /path/to/videos
```

### Command-line flags

```
-d, --dry-run              Show what would be processed (default)
-w, --wetrun               Actually perform encoding
-s, --size-threshold <MB>  Minimum file size in MB (default: 500)
-o, --override             Re-encode files already in HEVC
    --delete               Delete original after successful encode
    --ignore-processed     Ignore goenc metadata (re-process success/discarded)
    --retry-failed         Retry files that previously failed encoding
    --stats-csv <file>     Output stats to CSV file
    --log <file>           Log output to file (appends)
-v, --verbose              Verbose logging
    --debug                Debug logging
-h, --help                 Show this help
```

## How it works

1. **Scan**: Recursively scans the input directory for video files
2. **Filter**: Skips files below size threshold and already-HEVC files (unless --override)
3. **Check metadata**: Reads goenc processing history from file metadata
4. **Analyze**: Uses ffprobe to extract metadata and determine encoding profile
5. **Encode**: Re-encodes video to HEVC, copies all other streams (audio, subs, etc.)
6. **Validate**: Compares file sizes
7. **Replace**: Only replaces original if new file is smaller (with --delete flag)
8. **Tag**: Writes processing status to file metadata for future runs

## Metadata tracking

goreenc writes metadata tags to video files to track processing history. This prevents wasting time re-encoding files that have already been attempted.

### How it works

After processing each file, goreenc writes metadata tags:
- `goreenc_status`: One of `success`, `discarded`, or `failed`
- `goreenc_timestamp`: When the file was processed

On subsequent runs, goreenc checks these tags and skips files accordingly:

| Status | Meaning | Default behavior | Override flag |
|--------|---------|------------------|---------------|
| `success` | Previously encoded and replaced | Skip | `--ignore-processed` |
| `discarded` | Previously encoded but larger | Skip | `--ignore-processed` |
| `failed` | Previous encode failed | Skip | `--retry-failed` |

### Examples

```bash
# First run - processes all files
./goreenc --wetrun --delete /media/videos

# Second run - skips already processed files
./goreenc --wetrun --delete /media/videos
# Output: "Already processed by goreenc (success at 2024-10-06T08:30:00)"

# Force re-processing of successful/discarded files
./goreenc --wetrun --delete --ignore-processed /media/videos

# Retry only failed encodes
./goreenc --wetrun --retry-failed /media/videos

# Retry everything (both successful and failed)
./goreenc --wetrun --ignore-processed --retry-failed /media/videos
```

### Benefits

- **Saves time**: Won't re-encode files you've already processed
- **Avoid duplicated work**: Won't retry files that produced larger outputs
- **Selective retry**: Can retry just failed files without re-doing successful ones
- **Persistent**: Metadata is stored in the file itself, survives moves/renames

## Encoding profiles

Profiles are automatically selected based on video resolution:

| Resolution | CRF | Preset | x265 params |
|------------|-----|--------|-------------|
| < 720p     | 20  | medium | pools=none:no-sao:aq-mode=3:max-merge=4 | Native resolution |
| 720p-1079p | 20  | medium | pools=none:no-sao:aq-mode=3:max-merge=4 | Native resolution |
| ≥ 1080p    | 22  | medium | pools=none:no-sao:max-merge=4 | **Downscaled to 1080p** |

**Note:** Videos with resolution higher than 1080p are automatically downscaled to 1080p while preserving aspect ratio. This saves significant space with minimal quality loss.

## What gets preserved

- All audio streams (copied, not re-encoded)
- All subtitle streams (image-based and text-based, copied as-is)
- All attachments (fonts, cover art, etc.)
- All metadata (languages, titles, dispositions, chapters)
- Stream ordering and default flags

Only the video stream is re-encoded to HEVC.

## Safety features

- **Dry-run by default**: Must explicitly use `--wetrun` to encode
- **Size validation**: Never replaces with larger files
- **Temp files**: Encodes to `/tmp/goreenc` first, validates before replacing
- **Skip HEVC**: Won't re-encode HEVC files unless `--override` is used
- **Error handling**: Continues batch processing even if individual files fail