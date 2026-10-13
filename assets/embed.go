// Package assets embeds files the binary needs when installed standalone.
package assets

import _ "embed"

//go:embed coaching.md
var Coaching string

//go:embed topics.json
var Topics []byte
