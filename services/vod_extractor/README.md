# VOD Extractor

Internal HTTP wrapper around `yt-dlp` for resolving public VOD playback metadata.

`media_server` calls `POST /extract` on the internal Docker network. Keeping `yt-dlp` here avoids shipping Python and extractor dependencies in the main media API image.

## API

- `GET /health`
- `POST /extract` with JSON body `{ "url": "https://..." }`

## Runtime limits

- `VOD_EXTRACTOR_TIMEOUT_MS` caps each yt-dlp run.
- `VOD_EXTRACTOR_MAX_OUTPUT_BYTES` caps stdout retained from yt-dlp.
- `VOD_EXTRACTOR_MAX_CONCURRENT` caps concurrent yt-dlp processes.
