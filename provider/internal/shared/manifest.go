package provider

import "sync"

// ManifestMu serialises writes to .anvil/build-manifest.json. Components are
// constructed concurrently in one provider process during build-mode discovery,
// and each writer does a read-modify-write of the same file.
var ManifestMu sync.Mutex
