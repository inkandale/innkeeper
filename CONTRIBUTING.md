# Contributing

Rules for code changes are in [AGENTS.md](AGENTS.md). Design:
[docs/superpowers/specs/2026-10-03-innkeeper-core-design.md](docs/superpowers/specs/2026-10-03-innkeeper-core-design.md).

## Development

Database tests need PostgreSQL with pgvector and a role that can create
databases. Without `INNKEEPER_TEST_DATABASE_URL` they are skipped locally and
fail in CI.

```bash
docker run -d --name innkeeper-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 pgvector/pgvector:pg18
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
go test -race ./...
```

To run the stack from your checkout, `docker compose up -d --build`;
`compose.override.yaml` builds the local source. To work on the web UI, see
[Web UI](README.md#web-ui).

### Card corpus

`testdata/corpus` holds real cards and golden files of what the codec,
SillyTavern and Lumiverse return for each. `TestCorpusHashes` checks that
every app's copy hashes like the codec's, reading only the goldens. After an
app or plugin upgrade, recapture them against throwaway servers, from the
repo root:

```bash
make -C ../innkeeper-plugins install DEST=/tmp/innkeeper-plugins
docker compose -f testdata/corpus/compose.yaml up -d --wait
INNKEEPER_CORPUS_CAPTURE=1 INNKEEPER_PLUGINS_DIR=/tmp/innkeeper-plugins go test ./internal/corpus -run TestCapture -count=1
docker compose -f testdata/corpus/compose.yaml down -v
```

The capture refuses to run against a server that already has cards or
lorebooks, so start the servers fresh each time. The golden diff shows any
change in an app's format; if `TestCorpusHashes` then fails, fix the cause
before committing the goldens.

## Releasing

Work lands on `develop`. Every push to `develop` publishes
`ghcr.io/inkandale/innkeeper:latest` and `:sha-<commit>`. Merging `develop`
into `main` updates release-please's release PR, so merge with a merge
commit, not a squash, to keep each commit's type. Merging the release PR tags
`v<version>`, publishes the image and the Helm chart, and attaches
`compose.yaml` and the `ink` binaries to the GitHub release. release-please
sets the version in `chart/Chart.yaml` and in `compose.yaml`'s image tag.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat:` and `fix:` decide the next version, and `feat!:` marks a breaking change.
