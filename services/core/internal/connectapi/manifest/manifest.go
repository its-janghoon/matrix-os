// Package manifest renders the set of RPC paths a node serves over HTTP.
//
// It exists so a client in another language cannot drift from the protos
// silently. The SDK in packages/sdk checks itself against the JSON this
// produces, and a Go test fails when the checked-in copy is stale - which is
// the failure that shipped a console client for a protocol the daemon did not
// serve.
package manifest

import (
	"encoding/json"
	"sort"

	"google.golang.org/grpc"
)

// Service is one service and the methods it serves, in a stable order.
type Service struct {
	Name    string   `json:"name"`
	Methods []string `json:"methods"`
}

// Manifest is the whole served surface.
type Manifest struct {
	// Note explains to a reader of the JSON where it came from.
	Note     string    `json:"note"`
	Services []Service `json:"services"`
}

// Build collects the descriptors into a manifest, sorted so the output is
// byte-stable across runs.
func Build(descs ...*grpc.ServiceDesc) Manifest {
	services := make([]Service, 0, len(descs))
	for _, desc := range descs {
		methods := make([]string, 0, len(desc.Methods))
		for _, m := range desc.Methods {
			methods = append(methods, m.MethodName)
		}
		sort.Strings(methods)
		services = append(services, Service{Name: desc.ServiceName, Methods: methods})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	return Manifest{
		Note: "Generated from the gRPC service descriptors by services/core/internal/connectapi/manifest. " +
			"Every entry is served at POST /{service}/{method} by a node's HTTP endpoint. " +
			"Do not edit by hand; run `go generate ./internal/connectapi/...`.",
		Services: services,
	}
}

// JSON renders the manifest as the pretty JSON the SDK checks in.
func JSON(m Manifest) ([]byte, error) {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
