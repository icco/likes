# likes

Serves every five-star book, movie, anime, and TV show from Goodreads, Letterboxd, Trakt, and AniList as one JSON document.

## Install

```sh
go install go.icco.me/likes@latest
```

Or run the image:

```sh
docker run -p 8080:8080 \
  -e DATABASE_URL=postgres://alice:secret@db:5432/likes \
  -e LETTERBOXD_USERNAME=alice -e GOODREADS_USER_ID=12345 \
  ghcr.io/icco/likes:main
```

## Usage

| Route | Does |
| --- | --- |
| `GET /` | Returns `{"books": [], "movies": [], "anime": [], "tv": []}`, newest first, with an `ETag`. |
| `GET /cron` | Refetches every source and saves the results to Postgres. Returns 502 if a source failed and 409 if a refresh is already running. |

The server also refreshes once at startup. Run `/cron` from cron, for example `curl -fsS https://likes.example.com/cron` hourly.

Each source is enabled by its environment variables:

| Variable | Source | Counts as five stars |
| --- | --- | --- |
| `LETTERBOXD_USERNAME` | Letterboxd RSS feed (movies) | 5.0 |
| `TRAKT_USERNAME`, `TRAKT_CLIENT_ID` | Trakt public ratings (movies, TV) | 10/10, or for a show, rated episodes averaging 9.5+ over at least 3 |
| `GOODREADS_USER_ID` | Goodreads "read" shelf RSS (books) | 5 |
| `ANILIST_USERNAME` | AniList public list (anime) | top of the user's score format |

`DATABASE_URL` is required. The server creates its `items` table on startup. `PORT` defaults to `8080`.

## Notes

- Letterboxd blocks scraping the full ratings pages, and its RSS feed holds only the latest diary entries. The Letterboxd source keeps every five-star film it has seen across runs, so history before the first run comes from Trakt.
- A movie rated on both sites is listed once, keyed on TMDb id, with the Letterboxd entry winning.
- A source that fails keeps its previous items until it next succeeds.
- Trakt needs only an API app's client ID for a public profile. Create one at [trakt.tv/oauth/applications](https://trakt.tv/oauth/applications).
