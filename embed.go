// Package opensop embeds the files the CLI ships with: the format reference printed by
// `opensop guide` and the skills installed by `opensop skills install`.
//
// The Go code lives under cmd/ and internal/; this root package exists so go:embed can
// reach FORMAT.md and skills/ without copies.
package opensop

import "embed"

// FormatMD is FORMAT.md.
//
//go:embed FORMAT.md
var FormatMD string

// Skills holds skills/<name>/... .
//
//go:embed skills
var Skills embed.FS
