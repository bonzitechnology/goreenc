# goreenc

A Go-based video reencoder that converts videos to HEVC (H.265) or AV1 while keeping audio, subtitles, chapters and metadata. Only replaces files when encoding saves space.

## Dependencies

- `ffmpeg` - video encoding, built with `libx265` (and `libsvtav1` for `--av1`)
- `ffprobe` - metadata extraction (usually bundled with ffmpeg)
- `file` - MIME type detection (standard on most Unix systems)

goreenc runs on Linux and macOS. The Windows build compiles but doesn't currently work: it relies on the `file` command and a `/tmp` directory.

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
  -o, --override             Re-encode files already in the target codec (or AV1, in HEVC mode)
      --delete               Replace original when the encode is smaller
      --av1                  Use AV1 codec instead of HEVC
      --4k                   Keep 4K resolution (disable downscaling)
      --quality <crf>        Override CRF quality (e.g. 18–28; lower = better)
      --preset <preset>      Override encoder preset (HEVC: slow, medium, fast...; AV1: -2 to 13)
      --min-savings <pct>    Keep an encode only if it saves at least this % (default: 0)
      --estimate             Estimate savings with sample encodes and skip files below --min-savings
      --preserve-timestamps  Preserve original file modification times
      --ignore-processed     Ignore goreenc metadata (re-process success/discarded)
      --retry-failed         Retry files that previously failed encoding
      --stats-csv <file>     Output stats to CSV file (appends)
      --log <file>           Log output to file (appends)
  -v, --verbose              Verbose logging (currently the default level)
      --debug                Debug logging
  -h, --help                 Show this help
```

## Behaviour

- Only the video stream is re-encoded. Audio, chapters, metadata and attachments such as fonts are copied as-is. Cover images are dropped: MP4 cover art, and image attachments in MKV files (ffmpeg treats those as video streams).
- Subtitles MKV supports (SRT, ASS/SSA, WebVTT, PGS, VobSub, DVB) are copied as-is. Other text formats (e.g. MP4 `mov_text`) are converted to SRT. Formats that can be neither (e.g. EIA-608, teletext) are dropped with a warning.
- HDR10 and HLG colour signalling is kept, including when downscaling. Dolby Vision profile 5 files are skipped, because re-encoding them breaks their colours. Other Dolby Vision profiles lose the DV layer and keep their HDR10/HLG/SDR base.
- Videos more than 1080 pixels tall are downscaled to 1080 pixels tall (aspect ratio preserved). Use `--4k` to disable.
- Files below the size threshold (default 500 MB) are skipped.
- Files already in HEVC or AV1 are skipped unless `--override` is set. With `--av1`, only AV1 files are skipped, so HEVC files get converted to AV1.
- Encoding happens in a per-run folder under `/tmp/goenc`, which is removed on exit. The original is only replaced if the new file is smaller and its duration matches the source (within 2s or 1%), so truncated encodes from corrupt sources are rejected.
- With `--delete`, the encode replaces the original as `<name>.mkv`. If a *different* file already has that name, it's saved as `<name>.hevc.mkv` (or `.av1.mkv`) instead of overwriting it.
- Without `--delete`, the encode is saved next to the original as `<name>.hevc.mkv` (or `.av1.mkv`) and the original is left alone.
- An encode replaces the original only if it saves at least `--min-savings` percent of the file size (default 0, i.e. any saving). Otherwise it's discarded.
- Dry-run is the default - you must pass `--wetrun` to modify anything.

## Savings estimate

With `--estimate`, goreenc encodes four 20-second sample clips (at 20/40/60/80% of the way through the file) with the real settings before committing to a full encode. If the estimated saving is below `--min-savings` (or negative, when no minimum is set), the file is marked as discarded without a full encode. This costs roughly 1% of a full encode for a feature-length film. Files shorter than 160 seconds aren't sampled.

Estimates are usually close, but not exact. When the source's audio bitrate isn't recorded in the file, the estimate assumes the whole file is video, which makes the predicted saving larger than it really is. That errs towards encoding, never towards skipping a file that would shrink. Whether or not a file was estimated, the full encode's real size is still checked against `--min-savings` before it replaces anything.

`--estimate` also works in dry-run mode, to preview estimates without modifying anything.

```bash
# Preview estimated savings across a library
./goreenc --estimate --min-savings 10 /media/videos

# Only re-encode files estimated to shrink by at least 10%
./goreenc --wetrun --delete --estimate --min-savings 10 /media/videos
```

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

goreenc records what it has done in each file's `comment` tag, in the form `goenc:<status>:<timestamp>`:

- Encoded files get the tag as part of the encode.
- Originals that aren't replaced (`failed`, `discarded`, or encoded without `--delete`) get the tag by remuxing them (stream copy, no re-encoding). The remux is written to a temp file and checked to have the same streams and duration before it replaces the original. Use `--preserve-timestamps` to keep the original's modification time.
- If the tag can't be written safely (unsupported container, corrupt file, read-only directory), the original is left untouched and the status goes in a hidden sidecar file next to it instead, e.g. `.movie.mp4.goenc`.

On subsequent runs, tagged files are skipped:

| Status | Meaning | Default | Override |
|--------|---------|---------|----------|
| `success` | Encoded (replaced, or saved alongside without `--delete`) | Skip | `--ignore-processed` |
| `discarded` | HEVC encode was larger or saved less than `--min-savings` | Skip in HEVC mode | `--ignore-processed` |
| `discarded-av1` | Same, for an AV1 encode | Skip in `--av1` mode | `--ignore-processed` |
| `failed` | Encode failed | Skip | `--retry-failed` |

A discard only blocks the codec it was recorded for, so a file HEVC couldn't shrink still gets an AV1 attempt with `--av1`, and vice versa. A file discarded under both codecs is tagged `discarded+discarded-av1`.

Tags live in the file itself, so they survive moves and renames. Sidecar files have to be moved along with their video.

```bash
# First run - processes everything
./goreenc --wetrun --delete /media/videos

# Second run - skips already processed files
./goreenc --wetrun --delete /media/videos

# Force re-process successful/discarded files
./goreenc --wetrun --delete --ignore-processed /media/videos

# Also retry previously failed encodes
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
make test               # run tests (there are none yet)
make fmt                # format code
make vet                # run go vet
make check              # fmt + vet + test
```
