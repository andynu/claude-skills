# claude-skills

Tiny TUI that links Claude Code skills from a source library into a
target `.claude/skills/` directory as symlinks (or junctions on Windows).

## Install

```bash
go build -o claude-skills .
# move it onto your PATH, e.g. ~/bin/claude-skills
```

Builds clean for Linux, macOS, and Windows from the same source. On
Windows it tries a directory symlink first and falls back to a junction
(`mklink /J`) so admin rights / Developer Mode aren't required.

## Usage

```bash
claude-skills                   # interactive picker
claude-skills -list             # print on/off state and exit
claude-skills -scope=project    # force <cwd>/.claude/skills
claude-skills -scope=user       # force ~/.claude/skills
```

Keys: `space`/`x` toggle, `enter` apply, `q`/`esc` cancel,
`j`/`k` or arrows to move, `g`/`G` jump to top/bottom. A `*` marker
means a pending change.

## Where skills get linked

By default the tool links into the `.claude/skills/` directory **closest
to the current working directory**:

1. If `<cwd>/.claude/` exists and is a directory → project scope.
2. Else if `~/.claude/` exists and is a directory → user scope.
3. Otherwise → error: create one of those `.claude/` directories first,
   or pass `-scope` to be explicit.

The `skills/` subfolder is auto-created on save when the parent `.claude/`
already exists; the tool will not create `.claude/` itself.

If `.claude` exists but is a regular file (not a directory), the tool
refuses to proceed.

## Source library

The first time you run `claude-skills` it walks you through a short
onboarding wizard, asking where your skill library should live (default
`~/claude-skills`, offering to create it). The answer is saved to a
config file so you only have to do this once:

- Linux:   `~/.config/claude-skills/config.json` (respects `$XDG_CONFIG_HOME`)
- macOS:   `~/Library/Application Support/claude-skills/config.json`
- Windows: `%AppData%\claude-skills\config.json`

The config file is a small JSON document:

```json
{
  "source_dir": "/home/you/claude-skills"
}
```

Edit it directly to change the source directory, or delete it to re-run
the wizard. To override per-invocation without touching the config:

```bash
CLAUDE_SKILLS_DIR=/some/path claude-skills
```

Each immediate subdirectory of the source (excluding entries starting
with `_` or `.`) is treated as one skill.

## Safety

- The tool only removes links it identified during load as
  symlinks/junctions pointing into the source library. Real files or
  directories sitting in `.claude/skills/` are never touched.
- Adding a skill refuses to overwrite an existing entry with the same
  name.
- Orphan links (pointing into the source but with the source dir
  removed) can be toggled off to clean up, but can't be re-enabled.
