# Third-party notices

The Go generator source is distributed under the Apache License 2.0 in [LICENSE](LICENSE).

The bundled archive in `snapshots/` comes from [aws-cloudformation/resource-provider-enhanced-schemas](https://github.com/aws-cloudformation/resource-provider-enhanced-schemas). AWS distributes that schema data under the MIT No Attribution (MIT-0) license, reproduced in [snapshots/LICENSE.aws](snapshots/LICENSE.aws). Generated property definitions and documentation derived from that data retain its upstream license.

The exact snapshot source, date, and checksum are recorded in [RESEARCH.md](RESEARCH.md) and `generated/manifest.json`. Go dependencies retain their respective upstream licenses.

The unmodified templates in `corpus/smoke/original/aws-examples/` come from
`aws-cloudformation/aws-cloudformation-templates` under Apache-2.0; templates in
`corpus/smoke/original/cfn-lint-good/` come from `aws-cloudformation/cfn-lint`
under MIT-0. Their upstream licenses are reproduced in `corpus/smoke/licenses/`.
Normalized copies retain those licenses. Exact commit IDs, paths, URLs, and
content hashes are recorded in `corpus/smoke/index.json` and
`corpus/sources.lock.json`. Project-authored mutation cases are Apache-2.0.

The AWS identifier-format patterns in `internal/generate/formats.go` are
derived from cfn-lint's MIT-0 format rules at the commit pinned by the corpus
source lock. They retain AWS's attribution in that source file. The JSON files
in `internal/generate/cfnlint/extensions/` are unmodified copies of cfn-lint's
MIT-0 extension schemas from the same commit; `internal/generate/extensions.go`
records their source and attribution. The upstream license is reproduced in
`corpus/smoke/licenses/cfn-lint-good/LICENSE`.

The `unslop` skill in `.agents/skills/unslop/` comes from [cursor/plugins](https://github.com/cursor/plugins/tree/837067b75aa20ab58d7b2a2f6c0531e37118c596/pstack/skills/unslop). It is copyright (c) 2026 Lauren Tan and distributed under the MIT license, reproduced in [.agents/skills/unslop/LICENSE](.agents/skills/unslop/LICENSE).
