# Public claims baseline

This inventory classifies protection, security, support, maturity and benchmark claims against [claims.yaml](claims.yaml).
Each surface below has a separate owner; pending surfaces are not yet inventoried here.
M0-07-DOCS corrects the public surfaces; `go test ./tests/contract/...` checks the inventory.
Finding references are ids only (R01-R15), never titles, descriptions or triggers.

## Pins
- cordum: `549772371995b9eb5a95a2c6c8685551e0231724` (claims.yaml sources_reviewed_at).
- Cordum-site: `fd06b5f131c89c1edd3f004f51ada3340125fb05`.

## Generator
Source this block, then run only your own surface's function into `docs/contract/public-claims/<surface>.hits`.
```bash
# Run in bash (Git Bash on Windows) from the cordum repository root.
# CORDUM and SITE are clones that contain the pins; SITE is needed only for site-* surfaces.
CORDUM=. SITE=/path/to/Cordum-site
PIN_C=549772371995b9eb5a95a2c6c8685551e0231724
PIN_S=fd06b5f131c89c1edd3f004f51ada3340125fb05
REGISTER=(-e 'enforc|guarantee|verif|tamper|sign(ed|ature)|hash|fail.?closed|fail.?open|den(y|ied)|block|approv|quarantin|redact|audit|provenance|safe|secur|zero.?trust|cannot|never|always|isolat|sandbox|limit|cosign|sbom|exactly.once|immutable|integrity|govern|protect|prevent|bypass|least.privilege|encrypt|tls|mtls|rbac|tenant|compliance|soc ?2|hipaa|gdpr')
ASSURANCE=(
  -e 'guarantee'
  -e 'tamper.?(proof|evident|resistant|free)'
  -e 'zero.?trust'
  -e '(cannot|can.?t|can not|never|always) (be )?(bypass|skip|forg|alter|modif|tamper|escape|override|spoof|leak|leave|reach|run|execut|expose|den|enforc|verif|block|redact|fail|see|touch|trust|read|write|access|exfil)'
  -e 'fail(s|ing)?.?(closed|safe)'
  -e 'default.?deny|deny.?by.?default|denied by default'
  -e 'exactly.once|at.most.once'
  -e 'immutable|append.only|write.once'
  -e 'cryptograph'
  -e 'signed (manifest|receipt|bundle|polic|image|artifact|release|audit|evidence|binar|attestation)'
  -e 'hash.?(bound|chain|binding)'
  -e 'enforced? (at|by|before|on every|in code|server.?side)|is enforced|are enforced|enforces? (every|all|policy|policies|the)'
  -e '(prevent|block|stop)(s|ed|ing)? (risky|destructive|unauthori|dangerous|malicious|sensitive|secret|pii|data|exfil|agents? from|any|every|all|bad)'
  -e 'least.privilege'
  -e 'soc ?2|hipaa|gdpr|iso ?27001|fedramp|pci.?dss|compliant'
  -e 'production.?(ready|grade|proven)|enterprise.?(grade|ready)|battle.?tested|mission.?critical|general availability'
  -e '[0-9][0-9.,]* ?(ms|us|ns|rps|krps|req/s|ops/s|jobs/s|tps)([^a-zA-Z]|$)|p9[59]([^0-9]|$)|[0-9]+x (faster|lower|less)'
  -e 'support(ed)? (platform|version|release|window|policy|tier|matrix|lifecycle)|end.of.life|long.term support'
  -e 'secure (by default|by design)|security (guarantee|boundary)|provably|formally verified'
  -e 'only (approved|authori[sz]ed|signed|verified|allowed) '
)
hits() { cut -d: -f2,3 | LC_ALL=C sort -u; }
cordum_top() {
  { git -C "$CORDUM" grep -n -I -i -E "${REGISTER[@]}" "$PIN_C" -- README.md SECURITY.md
    git -C "$CORDUM" grep -n -I -i -E "${ASSURANCE[@]}" "$PIN_C" -- ROADMAP.md BENCHMARKS.md SUPPORT.md; } | hits; }
cordum_docs_1() { git -C "$CORDUM" grep -n -I -i -E "${ASSURANCE[@]}" "$PIN_C" -- ':(glob)docs/*' docs/mcp docs/adr | hits; }
cordum_docs_2() { git -C "$CORDUM" grep -n -I -i -E "${ASSURANCE[@]}" "$PIN_C" -- docs ':(exclude,glob)docs/*' ':(exclude)docs/mcp' ':(exclude)docs/adr' | hits; }
site_pages() { git -C "$SITE" grep -n -I -i -E "${ASSURANCE[@]}" "$PIN_S" -- site/src site/public/llms-full.txt docs-site/docs ':(exclude)site/src/app/blog' ':(exclude)*.test.*' | hits; }
site_blog_1() { git -C "$SITE" grep -n -I -i -E "${ASSURANCE[@]}" "$PIN_S" -- 'site/src/app/blog/[0-9]*' 'site/src/app/blog/ai-agent-[a-r]*' ':(exclude)*.test.*' | hits; }
site_blog_2() { git -C "$SITE" grep -n -I -i -E "${ASSURANCE[@]}" "$PIN_S" -- site/src/app/blog ':(exclude)site/src/app/blog/[0-9]*' ':(exclude)site/src/app/blog/ai-agent-[a-r]*' ':(exclude)*.test.*' | hits; }
# Then generate only your own surface, for example:
#   cordum_top > docs/contract/public-claims/cordum-top.hits
```

## Surfaces
| Surface | Repo | Paths (short form) | Hits | Files | Owner | Accurate | Overstated | Unverifiable | Not-a-claim |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| cordum-top | cordum | README, SECURITY, ROADMAP, BENCHMARKS, SUPPORT | 193 | 4 | M0-07 | 38 | 52 | 54 | 55 |
| cordum-docs-1 | cordum | docs root, mcp, adr | 230 | 59 | M0-07-CORE-1 | pending | pending | pending | pending |
| cordum-docs-2 | cordum | remaining docs | 262 | 70 | M0-07-CORE-2 | pending | pending | pending | pending |
| site-pages | Cordum-site | src except blog/tests, llms-full, docs-site | 390 | 101 | M0-07-SITE-1 | pending | pending | pending | pending |
| site-blog-1 | Cordum-site | numbered and ai-agent-[a-r] blogs | 301 | 57 | M0-07-SITE-2 | pending | pending | pending | pending |
| site-blog-2 | Cordum-site | remaining blogs except tests | 376 | 62 | M0-07-SITE-3 | pending | pending | pending | pending |
| Total (M0-07-SITE-3 to fill per repo and class) | both | all surfaces | 1752 | 353 | M0-07-SITE-3 | pending | pending | pending | pending |

Other root *.md files (CHANGELOG.md, CONTRIBUTING.md and the like) and site test files are outside the epic's surface list.

## Entry format and legend
Each surface pairs a generated `<surface>.hits` (one `path:line` per hit) with a classified `<surface>.md`.
The prefix supplies the repo: `cordum-` means cordum, `site-` means Cordum-site.
Classification tables use `| File | Entries |`; a file may take several rows.
Separate entries with `;`: `<line> <ref> <class> [codes...]` or `<line> not-a-claim <reason>`.
Refs are `CLM-NNN`, `none:bench`, `none:support` or `none:maturity`; compliance/certification claims take a CLM.
- **accurate**: no stronger than the register status and boundary; carries no code.
- **overstated**: asserts stronger protection or omits a material boundary; requires a finding id or evidence code.
- **unverifiable**: repository evidence does not establish the claim either way; requires an evidence code or finding id.
- Codes: R01-R15, `ev:test`, `ev:bench`, `ev:release`, `ev:attest`, `ev:code`, `ev:policy`.
- Evidence codes identify missing test, benchmark, release, attestation, source-code or policy evidence respectively.
- Non-claim reasons: `instr` reader instruction; `code` command/config/identifier; `nav` heading/link/navigation;
  `plan` roadmap item; `limit` stated limitation; `ext` external system; `process` reporting/license/contribution process.
A line may carry several claims; a not-a-claim entry must stand alone on its line.
Class counts count entries, not distinct hit lines.

## Register rules
Add one public source `<repo>:<path>:<first entry line>` per (claim, file); preserve existing cited hit-line mappings.
Only add an entry for an existing protection claim not covered by the register; do not introduce public claims.
Reserved ids: M0-07 CLM-027..049; CORE-1 050..069; CORE-2 070..089; SITE-1 090..109; SITE-2 110..129; SITE-3 130..149.
Do not change existing statement, boundary, status, invariants, findings, enforcement points, tests or evidence.

## Checks
- `surface-pair`: both .md and .hits exist for every surface.
- `surface-name`: names match `^(cordum|site)-[a-z0-9]+(-[a-z0-9]+)*$`; `surface-none`: at least one surface exists.
- `row-format`: exactly two cells and a canonical repo-relative path.
- `entry-format`: positive line number and known reference/class/code/reason grammar.
- `claim-unknown`: every CLM reference resolves in the register.
- `class-codes`: accurate has no codes; overstated/unverifiable have codes.
- `entry-duplicate`: unique (path, line, ref); not-a-claim cannot share a line.
- `hits-format`: each manifest line is a canonical path and positive line number.
- `hit-missing` / `hit-extra`: classified locations equal the manifest set.
- `source-unfilled`: every known-claim entry has a public source for the same repo and file.
- `source-unmatched`: every register-cited hit has that claim's entry; non-hit context lines are exempt.
CRLF is accepted. `TestPublicClaimsBaseline` runs under `go test ./...` and logs per-surface/class counts with `-v`.

## Drift
Fetch each repo, then run `git -C <clone> diff --stat <pin> origin/main -- <surface paths>`.
Empty output means the baseline also covers fresh origin/main.
Otherwise the row's completion note lists changed files for M0-07-DOCS; pinned counts are not claims of current coverage.
