package parley

import "embed"

// PluginFiles ships the Herdr manifest and agent skill with every CLI installation.
//
//go:embed herdr-plugin.toml skills/parley/SKILL.md skills/parley/scripts/ensure-parley
var PluginFiles embed.FS
