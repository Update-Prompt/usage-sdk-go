# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Before you start

The SDK does one job and keeps it small: count calls and send the counts. Read the [usage SDK specification](https://github.com/Update-Prompt/usage-sdk-spec) first; changes that add rules or settings the spec leaves to the Update Prompt services are unlikely to be merged. For a larger change, open an issue to discuss it first.

## Making a change

1. Use the Go version in `go.mod`. The core package uses the standard library only.
2. Add or update tests. Each test has a Given / When / Then comment, and helpers go at the bottom of the file under a `Helpers` separator.
3. Run the checks CI runs:

   ```sh
   go mod tidy
   go vet ./...
   go test -race ./...
   golangci-lint run ./...   # v2, with .golangci.yml
   ```

4. Never commit real API keys or tokens, even in tests. Use obviously fake values such as `up_usage_test`. CI scans every commit for secrets.

By contributing, you agree that your contribution is licensed under the [Apache License 2.0](LICENSE).
