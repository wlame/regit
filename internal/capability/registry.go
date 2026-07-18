package capability

import (
	"strings"

	"github.com/wlame/regit/internal/strategy"
)

// registry is the ordered single source of truth for every non-interactive
// action. Each row carries the command's metadata and its headless Run. New
// verbs append a row here.
var registry = []Command{
	{
		Name:     "split",
		Summary:  "Split one commit into 2–4 semantic commits using an LLM.",
		Mutating: true,
		Positional: &PositionalSpec{
			Name: "commit-ish", Help: "the commit to split — hash, HEAD~n, or branch (default: HEAD)",
		},
		Workspace: WSRestructure,
		Selection: SelSingle,
		Params: []Param{
			{Name: "llm", Type: ParamString, Help: "provider/model, e.g. ollama/qwen2.5-coder"},
			{Name: "agent", Type: ParamString, Help: "agent CLI to drive instead of HTTP"},
			{Name: "agent-args", Type: ParamString, Help: "replace default agent args; {schema} is injected"},
			{Name: "llm-base-url", Type: ParamString, Help: "override the OpenAI-compatible endpoint"},
			{Name: "max-commits", Type: ParamInt, Default: "0", Help: "clamp on resulting commits (0 = config/auto)"},
			{Name: "prompt", Type: ParamString, Help: "short guidance passed to the LLM"},
			{Name: "test-cmd", Type: ParamString, Help: "validate each intermediate commit before applying"},
			{Name: "no-cache", Type: ParamBool, Default: "false", Help: "ignore any cached proposal"},
			{Name: "clear-cache", Type: ParamBool, Default: "false", Help: "delete the split cache and exit"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply the split (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
		},
		Examples: []Example{
			{Cmd: "regit split HEAD --llm=ollama/qwen2.5-coder", Why: "preview a split of HEAD (dry-run)"},
			{Cmd: "regit split HEAD --llm=ollama/qwen2.5-coder --execute", Why: "apply the split"},
			{Cmd: "regit split HEAD --yes", Why: "deprecated: --yes is an alias for --execute", Bad: true},
		},
		Limitations: []string{
			"Requires an LLM (--llm or --agent).",
			"Skips commits that are too small, single-hunk, pure-rename, or binary-only.",
		},
		ExitCodes: map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply/LLM error"},
		Run:       runSplit,
	},
	{
		Name:     "reword",
		Summary:  "Regenerate commit message(s) with an LLM (one proposal headless).",
		Mutating: true,
		Positional: &PositionalSpec{
			Name: "commit-ish", Help: "the commit(s) to reword (default: HEAD)", Variadic: true,
		},
		Workspace: WSRestructure,
		Selection: SelSingle,
		Params: []Param{
			{Name: "llm", Type: ParamString, Help: "provider/model, e.g. ollama/qwen2.5-coder"},
			{Name: "agent", Type: ParamString, Help: "agent CLI to drive instead of HTTP"},
			{Name: "agent-args", Type: ParamString, Help: "replace default agent args"},
			{Name: "agent-scan", Type: ParamBool, Default: "false", Help: "agent may browse repo files read-only for extra context (requires --agent)"},
			{Name: "llm-base-url", Type: ParamString, Help: "override the OpenAI-compatible endpoint"},
			{Name: "prompt", Type: ParamString, Help: "short guidance passed to the LLM"},
			{Name: "context", Type: ParamStringList, Help: "context keys to include, e.g. readme,filestates"},
			{Name: "context-lines", Type: ParamInt, Default: "0", Help: "git diff -U depth (0 = config default)"},
			{Name: "no-cache", Type: ParamBool, Default: "false", Help: "ignore cached proposals"},
			{Name: "clear-cache", Type: ParamBool, Default: "false", Help: "delete the reword cache and exit"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply the reword (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
		},
		Examples: []Example{
			{Cmd: "regit reword HEAD --llm=ollama/qwen2.5-coder", Why: "preview a new message for HEAD"},
			{Cmd: "regit reword HEAD~2 HEAD --llm=openai/gpt-4o --execute", Why: "reword two commits and apply"},
			{Cmd: "regit reword --agent-scan HEAD", Why: "--agent-scan without --agent is an error", Bad: true},
		},
		Limitations: []string{
			"Headless always requests exactly one variant (the TUI browses several).",
			"--agent-scan requires --agent.",
		},
		ExitCodes: map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply/LLM error"},
		Run:       runReword,
	},
	{
		Name:     "squash",
		Summary:  "Squash consecutive commits into one per chain (LLM-summarized message).",
		Mutating: true,
		Positional: &PositionalSpec{
			Name: "range", Help: "chain spec(s), git range semantics: A^..B includes A; comma- or space-separated for several chains",
			Required: true, Variadic: true,
		},
		Workspace: WSRestructure,
		Selection: SelSet,
		Params: []Param{
			{Name: "llm", Type: ParamString, Help: "provider/model, e.g. ollama/qwen2.5-coder"},
			{Name: "agent", Type: ParamString, Help: "agent CLI to drive instead of HTTP"},
			{Name: "agent-args", Type: ParamString, Help: "replace default agent args; {schema} is injected"},
			{Name: "llm-base-url", Type: ParamString, Help: "override the OpenAI-compatible endpoint"},
			{Name: "with-diffs", Type: ParamBool, Default: "false", Help: "include each commit's diff in the LLM input"},
			{Name: "prompt", Type: ParamString, Help: "short guidance passed to the LLM"},
			{Name: "no-cache", Type: ParamBool, Default: "false", Help: "ignore any cached summary"},
			{Name: "clear-cache", Type: ParamBool, Default: "false", Help: "delete the squash cache and exit"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply the squash (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
		},
		Examples: []Example{
			{Cmd: "regit squash 6f3a^..a2df", Why: "preview squashing a chain (dry-run); ^ includes 6f3a"},
			{Cmd: "regit squash da24bc9b..685199d3,687114d3,6ffd7976..a2df4cc2 --execute", Why: "squash several chains at once"},
			{Cmd: "regit squash 6f3a^..a2df --llm=ollama/qwen2.5-coder --with-diffs --execute", Why: "LLM summary using diffs, then apply"},
			{Cmd: "regit squash da24bc9b..685199d3", Why: "git semantics: A..B EXCLUDES da24bc9b — use da24bc9b^..685199d3 to include it"},
		},
		Limitations: []string{
			"A..B uses git semantics: commits AFTER A through B (A excluded). Use A^..B to include A.",
			"Ignores singletons (a selected commit with no selected neighbor).",
			"Cannot squash across a merge commit.",
			"Works without an LLM (first commit's message is the default); --llm/--agent enables a summary.",
		},
		ExitCodes: map[int]string{0: "ok / dry-run / nothing to squash", 1: "usage error", 2: "guard blocked", 3: "apply/LLM error"},
		Run:       runSquash,
	},
	{
		Name:      "redate",
		Summary:   "Spread commit/author dates across a calendar using a strategy.",
		Mutating:  true,
		Workspace: WSDates,
		Selection: SelActive,
		Run:       runRedate,
		Params: []Param{
			{Name: "strategy", Type: ParamEnum, Enum: strategyNames(), Required: true, Help: "day-assignment strategy"},
			{Name: "time", Type: ParamEnum, Enum: timeModelNames(), Help: "time-of-day model (default: config)"},
			{Name: "start", Type: ParamDate, Help: "first calendar day of the range"},
			{Name: "end", Type: ParamDate, Help: "last calendar day of the range"},
			{Name: "weekdays", Type: ParamStringList, Help: "eligible weekdays, e.g. mon,tue,wed (default: configured weekdays)"},
			{Name: "from", Type: ParamString, Help: "freeze history before this rev (default: all)"},
			{Name: "commits", Type: ParamString, Help: "target commits: A..B, a single ref, or a comma-separated list (default: whole active range)"},
			{Name: "seed", Type: ParamInt, Default: "0", Help: "RNG seed for randomized strategies"},
			{Name: "link-dates", Type: ParamBool, Default: "true", Help: "keep committer date == author date"},
			{Name: "pin", Type: ParamKeyVals, AppliesIf: "strategy=Manual-Pin", Required: true, Help: "DAY=N per-day quotas"},
			{Name: "pick", Type: ParamStringList, AppliesIf: "strategy=Picked-Days", Required: true, Help: "DAY,DAY chosen days"},
			{Name: "per-day-min", Type: ParamInt, AppliesIf: "strategy=Rate-Fill", Help: "min commits per active day"},
			{Name: "per-day-max", Type: ParamInt, AppliesIf: "strategy=Rate-Fill", Help: "max commits per active day"},
			{Name: "offset-days", Type: ParamInt, AppliesIf: "strategy=Offset-Shift", Help: "shift every commit by N days"},
			{Name: "offset-hours", Type: ParamInt, AppliesIf: "strategy=Offset-Shift", Help: "shift every commit by N hours"},
			{Name: "anchor", Type: ParamDate, AppliesIf: "strategy=Preserve-Relative", Required: true, Help: "new instant for the first commit"},
			{Name: "scale", Type: ParamFloat, AppliesIf: "strategy=Preserve-Relative", Default: "1", Help: "stretch/compress original spacing"},
			{Name: "deadline", Type: ParamDate, AppliesIf: "strategy=Backwards-from-Deadline", Required: true, Help: "last day to work backwards from"},
			{Name: "compress-day", Type: ParamDate, AppliesIf: "strategy=Compress", Required: true, Help: "single target day"},
			{Name: "time-fixed", Type: ParamClock, AppliesIf: "time=Fixed-time", Required: true, Help: "HH:MM for the Fixed-time model"},
			{Name: "jitter-min", Type: ParamInt, AppliesIf: "time=Jitter", Help: "± minutes for the Jitter model"},
			{Name: "workday-start", Type: ParamClock, Help: "HH:MM (default: config)"},
			{Name: "workday-end", Type: ParamClock, Help: "HH:MM (default: config)"},
			{Name: "lunch-start", Type: ParamClock, Help: "HH:MM lunch-gap start"},
			{Name: "lunch-end", Type: ParamClock, Help: "HH:MM lunch-gap end"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "allow-merges", Type: ParamBool, Default: "false", Help: "proceed even if the range contains merge commits (their side branches keep their dates)"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
		},
		Examples: []Example{
			{Cmd: "regit redate --strategy=Even-Spread --time=Working-hours --start=2026-01-01 --end=2026-03-01", Why: "preview an even spread across two months"},
			{Cmd: "regit redate --strategy=Manual-Pin --pin=2026-03-02=2,2026-03-05=1 --execute", Why: "pin specific days and apply"},
			{Cmd: "regit redate --strategy=Manual-Pin --start=2026-01-01", Why: "Manual-Pin without --pin is rejected", Bad: true},
			{Cmd: "regit redate --strategy=Even-Spread --start=2026-01-01 --end=2026-03-01 --execute", Why: "a range containing a merge commit is refused without --allow-merges", Bad: true},
		},
		Limitations: []string{
			"Strategy/time names come from strategy.Strategies()/TimeModels(); accepts kebab or quoted spaced.",
			"start/end are validated by the engine per strategy, not by --help.",
		},
		ExitCodes: map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply error"},
	},
	{
		Name:      "retitle",
		Summary:   "Rewrite commit subjects with a regex across a range.",
		Mutating:  true,
		Workspace: WSRestructure,
		Selection: SelActive,
		Run:       runRetitle,
		Params: []Param{
			{Name: "match", Type: ParamString, Required: true, Help: "RE2 regex matched against each subject"},
			{Name: "replace", Type: ParamString, Help: "replacement (supports $1 capture refs)"},
			{Name: "commits", Type: ParamString, Help: "rev-range A..B or a single ref (default: all)"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
		},
		Examples: []Example{
			{Cmd: `regit retitle --match='^feat: ' --replace=''`, Why: "strip a conventional-commit prefix (preview)"},
			{Cmd: `regit retitle --match='(?i)wip' --replace='WIP' --commits=HEAD~5..HEAD --execute`, Why: "normalize WIP markers on the last 5 commits"},
		},
		Limitations: []string{"RE2 syntax (no backreferences). Subjects that would become empty are skipped."},
		ExitCodes:   map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply error"},
	},
	{
		Name:      "reauthor",
		Summary:   "Rewrite the author (and by default committer) identity of commits.",
		Mutating:  true,
		Workspace: WSAuthorship,
		Selection: SelSet,
		Run:       runReauthor,
		Params: []Param{
			{Name: "name", Type: ParamString, Help: "new author name (omit to keep each commit's name)"},
			{Name: "email", Type: ParamString, Help: "new author email (omit to keep each commit's email)"},
			{Name: "committer", Type: ParamBool, Default: "true", Help: "also set committer identity to the new author"},
			{Name: "commits", Type: ParamString, Help: "A..B range, a single ref, or a comma-separated list (default: all)"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
		},
		Examples: []Example{
			{Cmd: "regit reauthor --name=wlame --email=new@x.com --commits=6f3a^..a2df", Why: "preview changing author+committer on a range"},
			{Cmd: "regit reauthor --email=new@x.com --commits=HEAD~3..HEAD --execute", Why: "change only emails on the last 3 commits; names preserved"},
			{Cmd: "regit reauthor --name=wlame --committer=false --commits=HEAD --execute", Why: "change only the author, leaving the committer untouched"},
			{Cmd: "regit reauthor --commits=HEAD --execute", Why: "neither --name nor --email given — rejected", Bad: true},
		},
		Limitations: []string{
			"At least one of --name/--email is required.",
			"--commits accepts a comma-separated list so an arbitrary TUI selection round-trips.",
			"Changes commit hashes for targets and their descendants; pushed history needs --force.",
			"Never changes file content (the original tree is reused and verified).",
		},
		ExitCodes: map[int]string{0: "ok / dry-run / nothing to change", 1: "usage error", 2: "guard blocked", 3: "apply error"},
	},
	{
		Name:      "drop-bodies",
		Summary:   "Strip the body of each commit in a range, keeping titles.",
		Mutating:  true,
		Workspace: WSRestructure,
		Selection: SelActive,
		Run:       runDropBodies,
		Params: []Param{
			{Name: "commits", Type: ParamString, Help: "rev-range A..B or a single ref (default: all)"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
		},
		Examples: []Example{
			{Cmd: "regit drop-bodies", Why: "preview dropping every commit body on the branch"},
			{Cmd: "regit drop-bodies --commits=HEAD~10..HEAD --execute", Why: "drop bodies on the last 10 commits"},
		},
		Limitations: []string{"Commits without a body are skipped. Titles are never changed."},
		ExitCodes:   map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply error"},
	},
	{
		Name:      "purge",
		Summary:   "Remove files or directories from branch history as if never added.",
		Mutating:  true,
		Workspace: WSRestructure,
		Selection: SelGlobal,
		Run:       runPurge,
		Positional: &PositionalSpec{
			Name: "path", Help: "repo-relative file or directory to purge (repeatable)",
			Variadic: true,
		},
		Params: []Param{
			{Name: "search", Type: ParamString, Help: "read-only: list history paths matching the query, then exit"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
		},
		Examples: []Example{
			{Cmd: "regit purge --search=secret", Why: "find candidate paths in history before purging"},
			{Cmd: "regit purge config/secrets.env", Why: "preview removing one file from all history"},
			{Cmd: "regit purge build/ old.log --execute", Why: "remove a directory and a file everywhere, applied"},
			{Cmd: "regit purge '*.log'", Why: "WRONG: globs are rejected; pass exact paths from --search", Bad: true},
		},
		Limitations: []string{
			"Blocks if a merge commit sits in the rewritten range, or if the path has history on a merged side branch at all (even a since-deleted \"pocket\" the branch itself never carries forward) — see git filter-repo.",
			"Exact repo-relative paths only (no globs); renamed ancestors are separate paths — use --search to find them.",
			"Branch-scoped: other branches, tags, reflog, and regit's own backup refs still reference the old objects until pruned (`git gc`). For whole-repo, all-refs scrubbing use `git filter-repo` — regit purge is the branch-scoped, preview-first, undoable member of the filter-repo/filter-branch family.",
			"The working tree is never touched: a purged path still on disk becomes untracked.",
			"Commits the purge empties are dropped; commits that were already empty are preserved.",
		},
		ExitCodes: map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply error"},
	},
	{
		Name:     "redact",
		Summary:  "Remove or replace a literal secret across branch history.",
		Mutating: true,
		// Grouped with purge: both are branch-scoped, preview-first history
		// rewrites keyed on repo-wide content (a path; a literal secret) rather
		// than an explicit commit selection, so the same Workspace/Selection
		// pairing fits. internal/ui's redact flow (flow.go's flowRedact) is
		// this row's featureBinding + panelRows entry.
		Workspace: WSRestructure,
		Selection: SelGlobal,
		Run:       runRedact,
		Params: []Param{
			{Name: "match", Type: ParamString, Required: true, Help: "literal secret text to find (NOT a regex)"},
			{Name: "in", Type: ParamStringList, Help: "glob(s) narrowing which paths are scanned"},
			{Name: "mode", Type: ParamEnum, Enum: []string{"remove", "replace"}, Default: "remove", Help: "remove the whole matched line, or replace within it"},
			{Name: "replace-pattern", Type: ParamString, AppliesIf: "mode=replace", Required: true, Help: "RE2 regex matched within each target line"},
			{Name: "replace-with", Type: ParamString, AppliesIf: "mode=replace", Help: "replacement text ($1 capture refs; omit to strip the match, keep the line)"},
			{Name: "no-cascade", Type: ParamBool, Default: "false", Help: "do not follow a hit's line forward through later history (rotations, renames)"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
			{Name: "sign", Type: ParamBool, Default: "false", Help: "re-sign rewritten commits with your configured signing key"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
		},
		Examples: []Example{
			{Cmd: "regit redact --match=sk-live-abc123", Why: "preview removing every line containing the literal secret (dry-run)"},
			{Cmd: `regit redact --match=sk-live-abc123 --mode=replace --replace-pattern='sk-live-\S+' --replace-with=REDACTED --execute`,
				Why: "replace only the secret substring (keeping the rest of the line) using capture-group-capable regex, then apply"},
			{Cmd: "regit redact --match=sk-live-abc123 --mode=replace --execute", Why: "--mode=replace without --replace-pattern is rejected: the rule is required", Bad: true},
			{Cmd: `regit redact --match='sk-.*'`, Why: "WRONG: --match is a literal substring (git pickaxe -S), not a regex — metacharacters are matched verbatim, not as a wildcard", Bad: true},
		},
		Limitations: []string{
			"Only the first-parent chain is rewritten; a match with history on a merged side branch is reported as an advisory note, never redacted from that branch's own commits — see gitio.SecretInSideHistory.",
			"Blocks if a merge commit sits in the rewritten range (like purge).",
			"--match is matched as a literal substring, never a regex; use --mode=replace with --replace-pattern for regex-based edits within a matched line.",
			"Headless has no picker: every occurrence Scan finds is redacted, including cascaded descendants (rotations/renames) unless --no-cascade.",
			"The secret value is written to .git/regit/command.log and shell history by this very invocation — rotate the credential after redacting.",
		},
		ExitCodes: map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply error"},
	},
	{
		Name:      "undo",
		Summary:   "Restore the branch to the most recent regit backup ref.",
		Mutating:  true,
		Workspace: WSNone,
		Selection: SelGlobal,
		Run:       runUndo,
		Params: []Param{
			{Name: "list", Type: ParamBool, Default: "false", Help: "list available backups (read-only)"},
			{Name: "to", Type: ParamString, Help: "restore a specific backup (ref name or short hash)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "unwind even if HEAD moved past the backup (discards newer commits)"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
		},
		Examples: []Example{
			{Cmd: "regit undo", Why: "restore the branch to the latest backup (acts immediately)"},
			{Cmd: "regit undo --list", Why: "see available backups before restoring"},
			{Cmd: "regit undo --force", Why: "unwind even though HEAD advanced past the backup's apply (discards the newer commits)"},
			{Cmd: "regit undo --execute", Why: "undo takes no --execute; it is exempt from dry-run", Bad: true},
		},
		Limitations: []string{
			"Exempt from dry-run: restores immediately. The replaced HEAD remains in the reflog.",
			"Restores only backups taken on the current branch; legacy (pre-branch-qualified) backups need an explicit --to=<ref>.",
			"Always creates a pre-undo backup of the current tip first, so the undo itself can be undone.",
			"Refuses (exit 2) when HEAD has advanced past the head the backup's apply produced, unless --force; backups from before this guard existed have no paired record and are never blocked.",
		},
		ExitCodes: map[int]string{0: "ok / nothing to undo", 1: "usage error", 2: "guard blocked (backup from another branch, or HEAD moved past the backup without --force)", 3: "restore error"},
	},
	{
		Name:      "install",
		Summary:   "Register regit as the `git re` subcommand (symlink git-re on PATH).",
		Mutating:  true,
		NoRepo:    true,
		Workspace: WSNone,
		Selection: SelGlobal,
		Run:       runInstall,
		Params: []Param{
			{Name: "force", Type: ParamBool, Default: "false", Help: "replace an existing git-re that does not point at regit"},
		},
		Examples: []Example{
			{Cmd: "regit install", Why: "link git-re next to the regit binary on PATH (applies immediately)"},
			{Cmd: "regit install --force", Why: "replace a foreign git-re file with the regit link"},
			{Cmd: "regit install --execute", Why: "install takes no --execute; it applies immediately and regit uninstall reverses it", Bad: true},
		},
		Limitations: []string{
			"Writes only the git-re symlink in the PATH directory containing regit; never touches any repository.",
			"Windows: creating symlinks may require elevated privileges; no copy/shim fallback is provided.",
		},
		ExitCodes: map[int]string{0: "ok / already installed", 1: "usage error", 3: "install error (regit not on PATH, unwritable directory, or foreign git-re without --force)"},
	},
	{
		Name:      "uninstall",
		Summary:   "Remove the `git re` integration (the git-re symlink).",
		Mutating:  true,
		NoRepo:    true,
		Workspace: WSNone,
		Selection: SelGlobal,
		Run:       runUninstall,
		Examples: []Example{
			{Cmd: "regit uninstall", Why: "remove the git-re link if it points at this regit binary"},
		},
		Limitations: []string{
			"Never deletes a git-re that does not resolve to the running regit binary.",
		},
		ExitCodes: map[int]string{0: "ok / not installed", 3: "uninstall error (foreign git-re, or removal failed)"},
	},
}

// Registry returns the ordered command table.
func Registry() []Command { return registry }

// Lookup returns the command with the given name and whether it exists.
func Lookup(name string) (Command, bool) {
	for _, c := range registry {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// strategyNames returns every day-strategy name in kebab form, registry order.
func strategyNames() []string {
	var out []string
	for _, s := range strategy.Strategies() {
		out = append(out, canonName(s.Name()))
	}
	return out
}

// timeModelNames returns every time-model name in kebab form, registry order.
func timeModelNames() []string {
	var out []string
	for _, tm := range strategy.TimeModels() {
		out = append(out, canonName(tm.Name()))
	}
	return out
}

// strategyByName resolves a CLI value (kebab or spaced, any case) to a Strategy,
// or nil if unknown.
func strategyByName(name string) strategy.Strategy {
	want := canonName(name)
	for _, s := range strategy.Strategies() {
		if canonName(s.Name()) == want {
			return s
		}
	}
	return nil
}

// timeModelByName resolves a CLI value to a TimeModel, or nil if unknown.
func timeModelByName(name string) strategy.TimeModel {
	want := canonName(name)
	for _, tm := range strategy.TimeModels() {
		if canonName(tm.Name()) == want {
			return tm
		}
	}
	return nil
}

// canonName canonicalizes a strategy/time-model name to the registry's kebab
// form: spaces → dashes, matched case-insensitively against the engine names so
// the returned value is always the exact casing the engine reports. Names that
// already contain dashes (e.g. "Preserve-Relative") are preserved.
func canonName(name string) string {
	kebab := strings.ReplaceAll(name, " ", "-")
	// Match case-insensitively against the real names so "even-spread" → "Even-Spread".
	for _, s := range strategy.Strategies() {
		real := strings.ReplaceAll(s.Name(), " ", "-")
		if strings.EqualFold(real, kebab) {
			return real
		}
	}
	for _, tm := range strategy.TimeModels() {
		real := strings.ReplaceAll(tm.Name(), " ", "-")
		if strings.EqualFold(real, kebab) {
			return real
		}
	}
	return kebab
}
