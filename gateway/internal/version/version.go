// Package version pins the product and protocol versions Kyurem is built
// against. Keeping them in one place makes the compatibility story explicit:
// TR-6 requires documenting which version of the OTel GenAI conventions and
// which A2A specification release the gateway targets.
package version

import "runtime/debug"

// Version is the Kyurem release version. Suffixed with -mN while
// milestones land; prefixed documentation lives in docs/.
const Version = "0.1.0-m1"

// SpecVersion is the A2A protocol specification version the gateway is
// built against. Must be kept in sync with the a2a-go SDK's a2a.Version.
const SpecVersion = "1.0"

// SDKVersion is the version of github.com/a2aproject/a2a-go/v2 in use.
// Filled lazily from the build info so it stays correct when the SDK is
// upgraded.
func GoVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/a2aproject/a2a-go/v2" {
				return dep.Version
			}
		}
	}
	return "unknown"
}
