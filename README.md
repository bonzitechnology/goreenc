# goreenc

A Go-based video reencoder that converts videos to HEVC (H.265) while preserving all streams and metadata. Only replaces files when encoding saves space.

## Dependencies

- `ffmpeg` - video encoding
- `ffprobe` - metadata extraction (usually bundled with ffmpeg)
- `file` - MIME type detection (standard on most Unix systems)

## Installation

```bash
make build       # build binary in current directory
make install     # install to /usr/local/bin
make uninstall   # remove from /usr/local/bin
```

Or directly with Go:

```bash
go build -o goreenc ./cmd/goreenc
```

## Usage

```bash
# Dry-run - show what would be processed (default, nothing is modified)
./goreenc /path/to/videos

# Actually encode
./goreenc --wetrun /path/to/videos

# Encode and replace originals (only if new file is smaller)
./goreenc --wetrun --delete /path/to/videos

# Full run - >4GB files only, replace originals, preserve timestamps, log everything
nice ./goreenc --wetrun --delete --preserve-timestamps \
  -s 4096 \
  --stats-csv output.csv \
  --log run.log \
  -v /path/to/videos
```

## Flags

```
  -d, --dry-run              Show what would be processed (default)
  -w, --wetrun               Actually perform encoding
  -s, --size-threshold <MB>  Minimum file size in MB (default: 500)
  -o, --override             Re-encode files already in HEVC/AV1
      --delete               Delete original after successful encode
      --av1                  Use AV1 codec instead of HEVC
      --4k                   Keep 4K resolution (disable downscaling)
      --preserve-timestamps  Preserve original file modification times
      --ignore-processed     Ignore goreenc metadata (re-process success/discarded)
      --retry-failed         Retry files that previously failed encoding
      --stats-csv <file>     Output stats to CSV file
      --log <file>           Log output to file (appends)
  -v, --verbose              Verbose logging
      --debug                Debug logging
  -h, --help                 Show this help
```

## Behaviour

- Only the video stream is re-encoded to HEVC. All audio, subtitles, attachments, and metadata are copied as-is. Embedded cover art is dropped.
- Subtitles MKV supports (SRT, ASS/SSA, WebVTT, PGS, VobSub, DVB) are copied as-is. Other text formats (e.g. MP4 `mov_text`) are converted to SRT. Formats that can be neither (e.g. EIA-608, teletext) are dropped with a warning.
- HDR10 and HLG colour signalling is kept, including when downscaling. Dolby Vision profile 5 files are skipped, because re-encoding them breaks their colours. Other Dolby Vision profiles lose the DV layer and keep their HDR10/HLG/SDR base.
- Videos above 1080p are automatically downscaled to 1080p (preserves aspect ratio). Use `--4k` to disable.
- Files below the size threshold (default 500 MB) are skipped.
- Already-HEVC files are skipped unless `--override` is set.
- Encoding happens in a per-run folder under `/tmp/goenc`, which is removed on exit. The original is only replaced if the new file is smaller and its duration matches the source (within 2s or 1%), so truncated encodes from corrupt sources are rejected.
- With `--delete`, the encode replaces the original as `<name>.mkv`. If a *different* file already has that name, it's saved as `<name>.hevc.mkv` (or `.av1.mkv`) instead of overwriting it.
- Without `--delete`, the encode is saved next to the original as `<name>.hevc.mkv` (or `.av1.mkv`) and the original is left alone.
- Dry-run is the default - you must pass `--wetrun` to modify anything.

## Encoding profiles

HEVC (default):

| Resolution | CRF | Preset | x265 params |
|------------|-----|--------|-------------|
| < 720p     | 20  | medium | `sao=0:aq-mode=3:max-merge=4` |
| 720p–1079p | 20  | medium | `sao=0:aq-mode=3:max-merge=4` |
| ≥ 1080p    | 22  | medium | `sao=0:max-merge=4` |

AV1 (`--av1`): CRF 30, preset 6 (svt-av1 uses a 0–63 CRF scale and numeric presets, so `--preset` must be a number from -2 to 13).

Output is always 4:2:0 at the source's bit depth: 8-bit sources become HEVC Main (8-bit), while 10-bit and HDR sources stay 10-bit (Main10) to avoid banding and keep HDR intact. 4:2:2 and 4:4:4 sources are converted to 4:2:0, because HEVC Rext profiles are poorly supported by players.

## Metadata tracking

goreenc records what it has done so later runs can skip files:

- Encoded files get a `comment` tag of the form `goenc:success:<timestamp>`.
- Originals that were not replaced (`failed`, `discarded`, or encoded without `--delete`) are never modified. Their status goes in a hidden sidecar file next to them, e.g. `.movie.mp4.goenc`.

On subsequent runs, tagged files are skipped:

| Status | Meaning | Default | Override |
|--------|---------|---------|----------|
| `success` | Encoded and replaced | Skip | `--ignore-processed` |
| `discarded` | Encoded but result was larger | Skip | `--ignore-processed` |
| `failed` | Encode failed | Skip | `--retry-failed` |

The `success` tag lives in the file itself and survives moves and renames. Sidecar files have to be moved along with their video.

```bash
# First run - processes everything
./goreenc --wetrun --delete /media/videos

# Second run - skips already processed files
./goreenc --wetrun --delete /media/videos

# Force re-process successful/discarded files
./goreenc --wetrun --delete --ignore-processed /media/videos

# Retry only failed encodes
./goreenc --wetrun --retry-failed /media/videos
```

## Other make targets

```bash
make build-linux        # Linux (amd64)
make build-linux-arm64  # Linux (arm64)
make build-windows      # Windows (amd64)
make build-macos        # macOS (amd64 + arm64)
make build-all          # all platforms
make build-debug        # with debug symbols
make clean              # remove build artifacts
make test               # run tests
make fmt                # format code
make vet                # run go vet
make check              # fmt + vet + test
```
