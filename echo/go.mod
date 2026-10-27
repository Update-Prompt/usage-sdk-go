module github.com/Update-Prompt/usage-sdk-go/echo

// The oldest Go this adapter supports: echo's own minimum. Builds and CI use
// the toolchain below.
go 1.25.0

toolchain go1.27.3

require (
	github.com/Update-Prompt/usage-sdk-go v0.1.0
	github.com/labstack/echo/v5 v5.4.2
)

require golang.org/x/time v0.15.0 // indirect

// Builds and tests here use the SDK next to this folder; apps that use the
// adapter get the version above
replace github.com/Update-Prompt/usage-sdk-go => ../
