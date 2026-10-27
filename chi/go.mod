module github.com/Update-Prompt/usage-sdk-go/chi

// The oldest Go this adapter supports. Builds and CI use the toolchain below.
go 1.24

toolchain go1.27.3

require (
	github.com/Update-Prompt/usage-sdk-go v0.1.0
	github.com/go-chi/chi/v5 v5.5.0
)

// Builds and tests here use the SDK next to this folder; apps that use the
// adapter get the version above
replace github.com/Update-Prompt/usage-sdk-go => ../
