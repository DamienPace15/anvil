package compliance

import _ "embed"

// DashboardHTML is the self-contained dashboard page served by
// `anvil compliance dashboard`. Everything (styles, script, logo) is inline so
// it works offline and loads nothing from third parties.
//
//go:embed dashboard/index.html
var DashboardHTML []byte
