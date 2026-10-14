# Project skills

## Unslop

`unslop` edits writing to remove common AI patterns. Its instructions are vendored
from [cursor/plugins, pstack/skills/unslop](https://github.com/cursor/plugins/tree/837067b75aa20ab58d7b2a2f6c0531e37118c596/pstack/skills/unslop)
at commit `837067b75aa20ab58d7b2a2f6c0531e37118c596`. The upstream MIT license is
included in [unslop/LICENSE](unslop/LICENSE).

The project keeps one copy in `.agents/skills/unslop/`:

- Codex and Cursor discover `.agents/skills/` directly.
- Claude Code discovers `.claude/skills/unslop`, a relative symlink to that copy.

All three tools can apply the skill automatically when a writing task matches its
description. The local `disable-model-invocation: false` setting enables this in
Claude Code and Cursor; `allow_implicit_invocation: true` in `agents/openai.yaml`
enables it in Codex. The writing instructions are unchanged from upstream.

You can also invoke it explicitly with `$unslop` in Codex or `/unslop` in Claude
Code and Cursor, followed by the text or file you want edited.

If the skill does not appear in an existing session, restart the agent or reload
the project. These discovery paths follow the [Codex skill documentation](https://learn.chatgpt.com/docs/build-skills),
[Cursor skill documentation](https://cursor.com/docs/skills), and
[Claude Code skill documentation](https://code.claude.com/docs/en/skills).

To update, replace `unslop/SKILL.md` from the desired upstream revision, include any
new supporting files and license changes, and update the commit link above. Keep
the local `agents/openai.yaml` and Claude symlink, and retain
`disable-model-invocation: false` in `SKILL.md` to allow automatic use.
