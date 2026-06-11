# Contributing to MuxCore Admin UI

## Development Setup

### Prerequisites

- Go 1.26.x ([download](https://go.dev/dl/))
- Tailwind CSS standalone CLI (`make css` downloads it automatically)
- golangci-lint ([install](https://golangci-lint.run/usage/install/)) — optional but recommended

### Clone and build

```bash
git clone https://github.com/Muxcore-Media/admin-ui.git
cd admin-ui
make build   # produces ./admin-ui
```

### Run against a local muxcored

```bash
# Terminal 1: start core in dev mode
cd ../core
MUXCORE_INSECURE_DISABLE_TLS=true ./muxcored

# Terminal 2: start admin UI
ADMIN_UI_INSECURE=true ./admin-ui
# → http://localhost:8080
```

### CSS development

```bash
make css    # Compile Tailwind CSS once
make css-watch  # Watch mode (requires tailwindcss CLI)
```

## Running Tests

```bash
make test   # go test with race detection
```

Tests must not depend on a running muxcored instance. Use mocks where needed.

## Linting

```bash
make lint   # golangci-lint if installed, else go vet
```

## Code Conventions

- **No comments explaining what the code does** — name things well instead.
- **Comments only for non-obvious WHY** — hidden invariants, workarounds.
- **No `os.Exit` from library code** — only `main` exits.
- **Structured logging via `log/slog`** — no `fmt.Println` in non-test code.
- **Context propagation** — every function that does I/O takes `ctx context.Context` as its first argument.
- **Error wrapping** — use `fmt.Errorf("operation %q: %w", name, err)`.
- **Templ components** — one `.templ` file per page/component. Keep handlers thin, templates dumber.

## Branch Naming

```
feat/<short-description>
fix/<short-description>
docs/<short-description>
refactor/<short-description>
```

## Pull Request Process

1. Branch from `main`.
2. Make your changes with tests.
3. Run `make ci` locally — it must pass.
4. Open a PR against `main`.
5. Squash-merge preferred.

## Security Vulnerabilities

Do **not** open a public issue. See [SECURITY.md](SECURITY.md) for the private reporting process.

## License

By contributing, you agree that your contributions will be licensed under the GPL-3.0 license.
