// Package retaskcli embeds shipped assets into the binary so commands can serve
// them without the source repository present. It exists at the module root
// because go:embed cannot reference files outside the embedding package's tree
// (e.g. ../skills), and skills/ lives at the repo root.
package retaskcli

import _ "embed"

// SkillMarkdown is the contents of skills/retask-cli.md, the Claude Code skill
// file. Embedding the canonical file (rather than a copy) keeps `retask skill`
// in sync with the published skill by construction.
//
//go:embed skills/retask-cli.md
var SkillMarkdown string
