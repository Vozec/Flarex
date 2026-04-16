[← Back to README](./README.md)

# Contributing

Thanks for considering a contribution!

## Quick start

```bash
git clone https://github.com/Vozec/FlareX
cd FlareX
make build           # builds bin/flarex + bin/mockworker
make test            # unit + integration
make test-race       # race detector
make bench           # E2E benchmark
make lint            # go vet (golangci-lint also configured)
```

## Pull requests

- One change per PR. Keep the scope tight.
- Run `make test-race` and `make lint` locally before pushing.
- Update tests for any logic change. Aim to keep package coverage from sliding.
- Mention any breaking config / CLI change in the PR description.

## Style

- Standard `gofmt` (Makefile target: `make fmt`).
- Comments in English, kept short. Prefer naming over comments.
- No emojis in code or commit messages.
- Errors wrapped with `%w` when forwarded.

## Test ergonomics

- All tests use stdlib `testing`. No assertion frameworks.
- Network-using tests must bind `127.0.0.1:0` (never public).
- Integration tests live alongside the package (`*_test.go` with `package X_test`).

## Reporting bugs

Open an issue with:
- Repro steps (CLI command, minimal config snippet).
- Expected vs actual behaviour.
- Logs at `log.level: debug` if relevant.
- Output of `flarex --version`.

## Security

Do not file public issues for security problems. Email the maintainer instead.
