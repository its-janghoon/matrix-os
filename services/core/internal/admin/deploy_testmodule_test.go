package admin

import "encoding/base64"

// testAgentConfig is a deployment config carrying a real, minimal WebAssembly
// module: the eight-byte magic-and-version header, which is a complete and
// valid module that declares nothing.
//
// These tests used to deploy with an empty config and assert success, which
// only passed because DeployAgent wrote a map entry saying "running" and
// executed nothing. Now that it actually loads and runs the module, a
// deployment needs a module - so the tests provide the smallest one there is.
// It compiles, instantiates, and has no _start to call, which is exactly what
// an authorization test wants: it exercises the whole path without making the
// test about what a guest does.
func testAgentConfig() map[string]interface{} {
	empty := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	return map[string]interface{}{
		"wasm_base64": base64.StdEncoding.EncodeToString(empty),
	}
}
