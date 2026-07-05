// Package examples embeds the shippable config templates so the CLI can
// scaffold a starter config (`init`) without a repo checkout.
package examples

import _ "embed"

//go:embed template-config.json
var TemplateConfig []byte
