---
name: commit
description: Drafts git commit messages from the current diff and repository context. Use when the user asks for a commit message, wants to commit staged or unstaged changes, or says "commit", "WIP commit", or "conventional commit".
---

# Git commit messages

## Workflow

1. Inspect what will be committed: run `git status` and `git diff` (use `git diff --staged` if the user is committing staged files only).
2. Summarize **intent** in one line; add a body only when the change needs explanation (why, trade-offs, breaking behavior).
3. Match the project’s existing style: scan recent messages with `git log -5 --oneline` and follow the same pattern (prefixes, tense, length).

## Quality bar

- **Subject**: imperative mood (“fix”, “add”, not “fixed”, “adds”). Roughly 50–72 characters; no trailing period.
- **Grammar**: Full sentences in the body; clear, factual, no filler.
- **Scope**: Mention the area touched when it helps (`input`, `rfb`, `example/server`, etc.).
- **Honesty**: Describe only what the diff does; flag uncertainty if the diff is ambiguous.

## Formats (pick one to match history)

**Conventional Commits** (when the repo already uses them or the user asks):

```text
type(scope): short description

Optional body with more detail.
```

Common types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`.

**Simple** (when history is plain):

```text
Short description of the change

Optional longer explanation.
```

## Output

Return a ready-to-paste block:

```text
git commit -m "subject line" -m "body paragraph if needed"
```

Or only the subject + body text if the user prefers to commit in the IDE.

## Do not

- Invent changes not visible in the diff.
- Use vague subjects (“update”, “fix stuff”, “changes”).
- Put breaking-change notes only in the subject; use the body or a `BREAKING CHANGE:` footer when appropriate.
