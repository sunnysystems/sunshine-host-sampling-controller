// Package buildinfo carries the controller's build identity.
//
// Version is stamped at link time by the Dockerfile:
//
//	-ldflags "-X github.com/sunnysystems/sunshine-host-sampling-controller/internal/buildinfo.Version=1.2.0"
//
// It lives in its own package so both main and the report client can read it
// without either importing the other. The "dev" default means an unstamped
// build (a local `go build`, or a Dockerfile that lost the flag) — Sunshine
// treats it as any other value: informational, never a gate.
package buildinfo

// Version is the controller's semver, without a leading "v". Overridden at link
// time; "dev" when unstamped.
var Version = "dev"

// UserAgent identifies the controller to Sunshine on every HTTP call.
func UserAgent() string {
	return "sunshine-host-sampling-controller/" + Version
}
