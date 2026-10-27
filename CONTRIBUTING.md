# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Before you start

The SDK does one job and keeps it small: count calls and send the counts. Read [SPEC.md](SPEC.md) first; changes that add rules or settings the spec leaves to the Update Prompt services are unlikely to be merged. For a larger change, open an issue to discuss it first.

## Making a change

1. Use the Go version in `go.mod`. The core package uses the standard library only. Each router adapter (in `chi/`, `gin/`, `echo/` and `gorillamux/`, packages `updatepromptchi` and so on, so they don't clash with the router's own package) is a module of its own, so its router is only downloaded by apps that use it. Its `replace` line builds it against the SDK in this repo.
2. Add or update tests. Each test has a Given / When / Then comment, and helpers go at the bottom of the file under a `Helpers` separator.
3. Run the checks CI runs, in every module:

   ```sh
   .github/scripts/each-module.sh go mod tidy
   .github/scripts/each-module.sh go vet ./...
   .github/scripts/each-module.sh go test -race ./...
   .github/scripts/each-module.sh golangci-lint run ./...   # v2, with .golangci.yml
   ```

4. Never commit real API keys or tokens, even in tests. Use obviously fake values such as `up_usage_test`. CI scans every commit for secrets.

## Releasing

Tag the SDK and every adapter at the same commit, with the same version: `v0.2.0`, `chi/v0.2.0`, `gin/v0.2.0`, `echo/v0.2.0` and `gorillamux/v0.2.0`. Before tagging, set the SDK version each adapter's `go.mod` requires, and `Version` in `client.go`, to that version.

By contributing, you agree that your contribution is licensed under the [Apache License 2.0](LICENSE).
