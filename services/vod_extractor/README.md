# VOD Extractor

Internal HTTP wrapper around `yt-dlp` for resolving public VOD playback metadata.

`media_server` calls `POST /extract` on the internal Docker network. Keeping `yt-dlp` here avoids shipping Python and extractor dependencies in the main media API image.

## API

- `GET /health`
- `POST /extract` with JSON body `{ "url": "https://..." }`
