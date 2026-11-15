#!/usr/bin/env python3
"""Credential sources reached transitively from GitHub workflow run steps through bash/sh/source/./exec/make edges
(bounded, repo-confined, symlink-refusing): each jq credential field or generated credential must be registered for
masking, fail-closed, before any use unless a reviewed exemption's guard and condition hold. Output carries no values."""
import collections, pathlib, posixpath, re, shlex, sys, yaml  # noqa: E401
MAX_DEPTH, MAX_VISITS, MAX_DRY, MAX_BYTES, UNKNOWN, ERREXIT, XTRACE = 10, 400, 1000, 256 * 1024, "\x00", ("e", "errexit"), ("x", "xtrace")  # UNKNOWN: set and non-empty, value unseen
HEREDOC, DOTENV = re.compile(r"(?<!<)<<-?\s*(['\"]?)([A-Za-z_]\w*)\1"), re.compile(r">\s*[\"']?(?:[^\s\"';&|<>]*/)?\.env[\"']?(?=[\s;&|<>]|$)")
PLAIN, DQ, ANY = re.compile(r"[^\s'\"\\$`<>&|;()#]+"), re.compile(r"[^\"\\$`\n]+"), re.compile(r".", re.S)
BRACE, REDIR, RWORD = re.compile(r"\$\{(?:[^{}]|\{(?:[^{}]|\{[^{}]*\})*\})*\}"), re.compile(r"&?(?:<<<|<<-?|<>|<&|>&|>>|>\||<|>)[ \t]*"), re.compile(r"(?:\d+|&)?[<>]")
SEPS, CI_TEST, RESERVED = ("&&", "||", ";;", "|&", ";", "|", "&", "\n", "(", ")"), r'\[\[ "\$\{GITHUB_ACTIONS:-\}" == "?true"? \]\]', {"!", "if", "elif", "while", "until", "then", "do", "else"}
ASSIGN, WORD_ASSIGN = re.compile(r"(?:(?:local|export|declare|typeset|readonly)\s+(?:-\w+\s+)*)?([A-Za-z_]\w*)=(.*)", re.S), re.compile(r"([A-Za-z_]\w*)=(.*)", re.S)
JQ, GEN = re.compile(r"\bjq\b.*?\.(?:\w*_)?(?:token|secret|password|api_key|key)\b(?!\.)", re.S), re.compile(r"openssl\s+rand\b|\buuidgen\b|/dev/u?random\b|\$\{?RANDOM\b|\$\(\s*date\b|secrets\.token_|\bpwgen\b")
NAME, PREFIX = re.compile(r"(?i)^(?!.*idem).*(?:passw|secret|token|key|nonce)"), re.compile(r"\$\{(\w+):(?:(\d+):(\d+)\}|\d|:|\s+-?\d|\(\s*-?\d)")
DEVNULL, TESTCMD, TOPLEVEL = re.compile(r"(?:^|\s)(?:1|&)?>>?\s*/dev/null(?=\s|$)"), re.compile(r"(?:(?:if|elif|while|until|then|do|else|!) )*(?:\[\[?|test) "), re.compile(r"\$\(\s*(?:pwd(?:\s+-P)?|git\s+rev-parse\s+--show-toplevel)\s*\)")
DIRNAME, VAR = re.compile(r"\$\(\s*dirname\s+(?:--\s+)?\"?\$\{?(?:BASH_SOURCE(?:\[0\])?|0)\}?\"?\s*\)|\$\{BASH_SOURCE(?:\[0\])?%/\*\}"), re.compile(r"\$\{([A-Za-z_]\w*)(?:(:?[-=])((?:[^{}]|\{[^{}]*\})*))?\}|\$([A-Za-z_]\w*)")
CDPWD, PREFIX_WORDS = re.compile(r"\$\(\s*cd\s+(?:--\s+)?(\"[^\"$]*\"|'[^']*'|[^\s;&|)$\"']+)\s*(?:>\s*/dev/null\s*)?(?:2>&1\s*)?&&\s*pwd(?:\s+-P)?\s*\)"), {"if", "then", "else", "elif", "do", "while", "until", "!", "time", "exec", "command", "builtin", "nohup", "env", "sudo", "timeout"}
Cmd = collections.namedtuple("Cmd", "line words op sub stack func text sets", defaults=((), "", "", ""))  # sets: what a file sourced here leaves set, e (errexit off) or E (errexit on), x (tracing on) or X (tracing off)
def seeded(env: dict, name: str) -> bool: return (value := env.get(name, "")) != "" and (value[0] != "$" or value.startswith("$("))
def literal(value: str) -> int: return len(re.match(r"[^$\x00]*", value).group())
def flag(cmd: Cmd, on: bool, short: str, name: str) -> bool: return short == "x" if short in cmd.sets else short == "e" if short.upper() in cmd.sets else next((sign == "-" for sign in reversed(re.findall(r"(?<!\S)([-+])(?:[a-zA-Z]*" + short + r"[a-zA-Z]*|o\s+" + name + r")(?!\S)", cmd.text))), on) if cmd.words[cmd.words[:1] in (["then"], ["do"], ["else"]):][:1] == ["set"] else on  # after a `set`
def pg_runs_gate_1(argv: list) -> bool: return "--skip-rebuild" not in argv and (not (gates := [v for f, v in zip(argv, argv[1:]) if f == "--gate"]) or not gates[-1].isdigit() or gates[-1] == "1")
EXEMPT = {  # reviewed exemptions: the source, a block around it, or every call site of its function matches the guard; the entry env holds the condition
    ("tools/scripts/edge_fake_hook_e2e.sh", "AGENTD_NONCE"): ("if ! want_bypass_hook", lambda env: env.get("CORDUM_EDGE_E2E_BYPASS_HOOK", "").lower() in ("1", "true", "yes", "on")),
    ("tools/scripts/quickstart.sh", "api_key"): (r'if \[\[ -z "\$\{api_key\}" \]\]', lambda env: seeded(env, "CORDUM_API_KEY")), ("tools/scripts/quickstart.sh", "REDIS_PASSWORD"): (r'if \[\[ -z "\$\{REDIS_PASSWORD:-\}" \]\]', lambda env: seeded(env, "REDIS_PASSWORD")),
    (".github/workflows/recover-dashboard-v1.2.0.yml", "token"): (re.escape("token=$(curl --fail --silent --show-error \"$token_url\" | jq -er '.token')"), lambda env: True)}  # anonymous public-image pull token: the exact fetch sends no credential, to literal URLs
PREFIX_EXEMPT = {("tools/scripts/quickstart.sh", "API_KEY"): lambda env, quiet, need: quiet or literal(env.get("CORDUM_API_KEY", "")) >= need}  # banner prefix: stdout discarded or seeded literal
GATES ={("tools/scripts/production_gate.sh", "gate_1_deploy", "tools/scripts/quickstart.sh"): pg_runs_gate_1}  # quickstart runs only in gate 1, never with --skip-rebuild
def heredocs(lines: list) -> list:  # (header index, body indices incl. terminator, quoted delimiter, terminated)
    found, index = [], 0
    while index < len(lines):
        if match := None if lines[index].lstrip().startswith("#") else HEREDOC.search(lines[index]):
            found.append((index, list(range(index + 1, min((end := next((k for k in range(index + 1, len(lines)) if lines[k].strip() == match.group(2)), len(lines))) + 1, len(lines)))), bool(match.group(1)), end < len(lines))); index = end  # noqa: E702
        index += 1
    return found
def split(text: str, i: int = 0, line: int = 1, sub: int = 0, closer: bool = False) -> tuple:  # (raw-word commands, substitution bodies first; end index or -1; line)
    cmds, words, word, start, quote, parens = [], [], "", line, "", 0
    def emit(op: str) -> None: nonlocal words, start; cmds.append(Cmd(start, words, op, sub)) if words or op in ("(", ")", "{", "}") or op in ("&&", "||", "|", "|&") and cmds and cmds[-1].op in (")", "}") else None; words, start = [], line  # noqa: E702  # keeps the operator after a block
    def push() -> None: nonlocal word; emit("\n") if word == "{" and words and set(words) <= RESERVED else None; emit(word) if word in ("{", "}") and (not words or word == "{" and (words[-1].endswith("()") or words[0] == "function")) else words.append(word) if word else None; word = ""  # noqa: E702  # a block's { or }, else a word
    while i < len(text):
        c, two = text[i], text[i:i + 2]
        if quote == "'" or two == "${":  # a single-quoted span, or a balanced ${...} in any quoting
            m = None if quote == "'" else BRACE.match(text, i)
            if (end := text.find("'", i) + 1 if quote == "'" else m.end() if m else 0) <= 0: return cmds, -1, line
            word, line, i, quote = word + text[i:end], line + text.count("\n", i, end), end, "" if quote == "'" else quote
        elif c == "\\": word, line, i = word + ("" if two == "\\\n" else two), line + (two == "\\\n"), i + 2
        elif c == "`" or two == "$(" or (two in ("<(", ">(") and not quote):
            tick = text.find("`", i + 1) if c == "`" else 0
            inner, end, _ = (split(text[i + 1:tick], 0, line, sub + 1) if tick > 0 else ([], -1, 0)) if c == "`" else split(text, i + 2, line, sub + 1, True)
            if (end := (tick + 1 if end >= 0 else -1) if c == "`" else end) < 0: return cmds, -1, line
            cmds += inner; word, line, i = word + text[i:end], line + text.count("\n", i, end), end  # noqa: E702
        elif quote == '"':
            chunk = (DQ.match(text, i) or ANY.match(text, i)).group()
            word, line, i, quote = word + chunk, line + chunk.count("\n"), i + len(chunk), "" if chunk == '"' else quote
        elif c in "'\"": word, quote, i = word + c, c, i + 1
        elif c == "#" and not word: i = text.find("\n", i) % (len(text) + 1)
        elif c in "<>" or two == "&>":
            if word and not word.isdigit(): push()
            word, i = word + (match := REDIR.match(text, i)).group().rstrip(), match.end()
        elif c in " \t": push(); i += 1  # noqa: E702
        elif two == "()": word, i = word + "()", i + 2
        elif sep := next((s for s in SEPS if text.startswith(s, i)), ""):
            push(); i += len(sep)  # noqa: E702
            if sep in ("&&", "||") and words[:1] == ["[["] and "]]" not in words: words.append(sep)
            elif sep == ")" and closer and not parens: emit("$)"); return cmds, i, line  # noqa: E702
            else:
                parens += {"(": 1, ")": -1}.get(sep, 0); emit("\n") if sep == "(" and words and set(words) <= RESERVED else None; emit(sep)  # noqa: E702  # reserved words before a ( or { end their own command
                if sep == "\n": line = start = line + 1
        else: word, i = word + (chunk := (PLAIN.match(text, i) or ANY.match(text, i)).group()), i + len(chunk)
    push(); emit(""); return cmds, -1 if quote or closer else i, line  # noqa: E702
def parse(text: str, line: int = 1) -> tuple:  # (commands with block stack and enclosing function, unparsable line)
    lines, error, cmds, stack = text.replace("\r\n", "\n").split("\n"), 0, [], []
    for head, body, quoted, closed in heredocs(lines):  # a heredoc written to .env becomes exports
        to_env, error = DOTENV.search(lines[head]), error or (0 if closed else head + line)
        for k in body:
            m = to_env and re.match(r"\s*([A-Za-z_]\w*)=(.*)$", lines[k])
            lines[k] = "export " + m.group(1) + "=" + (shlex.quote(m.group(2)) if quoted else '"' + re.sub(r'(["\\])', r"\\\1", m.group(2)) + '"') if m else ""
    raw, end, _ = split("\n".join(lines), 0, line)
    for cmd in raw:
        first = (cmd.words + [""])[cmd.words[:1] == ["!"]]  # `! if ...` opens an if
        if stack and stack[-1][1] in ({"fi": ("if",), "done": ("loop",), "esac": ("case",)}.get(first) or (("{", "func") if cmd.op == "}" and not cmd.words else ())): stack.pop()
        cmds.append(cmd._replace(stack=tuple(s[0] for s in stack), func=next((s[2] for s in reversed(stack) if s[2]), ""), text=" ".join(cmd.words)))
        if cmd.op == ")" and first != "case" and stack and stack[-1][1] == "(": stack.pop()  # not a `case ... in pattern)` line
        kind = "if" if first == "if" else "case" if first == "case" else "loop" if first in ("while", "until", "for", "select") else cmd.op if cmd.op in ("{", "(") else ""
        name = (cmd.words[1] if first == "function" and len(cmd.words) > 1 else first).removesuffix("()") if kind == "{" and cmd.words else ""
        if kind: stack.append((len(cmds) - 1, "func" if name else kind, name))
    return cmds, error or (line if end < 0 else 0)
def runs(cmds: list, i: int, floor: int = 0, sub: int = 0) -> bool: return cmds[i].sub == sub and cmds[i].op not in ("|", "|&", "&") and next((cmds[h].op for h in range(i - 1, floor - 1, -1) if cmds[h].sub == sub), "") not in ("&&", "||", "|", "|&")  # whenever its block does, in that block's own shell: not in a (deeper) $(...), a pipeline or the background, nor behind && or || from floor
def ends(cmds: list, i: int) -> str: return "" if (j := next((h for h in range(i + 1, len(cmds)) if i not in cmds[h].stack), len(cmds)) + (cmds[i].op == "{") if i + 1 < len(cmds) and i in cmds[i + 1].stack else i) >= len(cmds) or j != i and not all(RWORD.match(w) for w in cmds[j].words[cmds[j].words[:1] in (["fi"], ["done"], ["esac"]):]) else ends(cmds, n) if cmds[j].op in ("|", "|&") and (n := next((h for h in range(j + 1, len(cmds)) if cmds[h].sub == cmds[j].sub), len(cmds))) < len(cmds) else cmds[j].op  # the operator after the pipeline from cmds[i], "" if unknown; a compound command ends at its closer
def cond(cmds: list, i: int) -> bool: return bool(st := cmds[i].stack) and (cmds[st[-1]].words + [""])[cmds[st[-1]].words[:1] == ["!"]] in ("if", "while", "until") and next((c.words[:1] for c in reversed(cmds[st[-1] + 1:i + 1]) if c.stack == st and c.words[:1] in (["then"], ["do"], ["else"], ["elif"])), ["elif"]) == ["elif"]  # in the condition of the if, elif, while or until it is directly in: no then, do or else at its level since
def hot(cmds: list) -> set: return {s for s in {s for c in cmds for s in c.stack} if not (cmds[s].op == "{" and cmds[s].words) and (cmds[s].words[:1] == ["!"] or bool(s) and set(cmds[s - 1].words) <= RESERVED and cmds[s - 1].words[-1:] == ["!"] or cond(cmds, s) or ends(cmds, s) in ("&&", "||"))}  # blocks bash runs ignoring errexit throughout: a compound command negated, in a condition, or in a pipeline not last in an && or || list
def exits(cmds: list, start: int, level: tuple, var: str, k: int) -> bool:  # exit at level before else/elif or leaving it, and run from start; return only once var is local to k's function or cleared there, by a command that runs
    fn, stop = next((s for s in reversed(cmds[k].stack) if cmds[s].op == "{" and cmds[s].words), -1), next((i for i in range(start, len(cmds)) if len(cmds[i].stack) < len(level) or cmds[i].stack == level and cmds[i].words[cmds[i].words[:1] == ["then"]:][:1] in (["else"], ["elif"], ["exit"], ["return"])), len(cmds))
    words = cmds[stop].words[cmds[stop].words[:1] == ["then"]:][:1] if stop < len(cmds) and cmds[stop].stack == level and runs(cmds, stop, start, cmds[k].sub) else []  # in the value's shell: not skipped, piped, backgrounded or captured
    held = fn >= 0 and any(c.stack[-1:] == (fn,) and c.words[:1] == ["local"] and var in [w.split("=")[0] for w in c.words[1:]] and runs(cmds, i) for i, c in enumerate(cmds[:k + 1]))  # the caller cannot read it after the return
    return words == ["exit"] or words == ["return"] and (held or any(c.stack == level and re.fullmatch(r"(?:then )?(?:unset (?:-v )?" + var + "|" + var + r"=(?:\"\"|'')?)", c.text) and runs(cmds, i) and not any(re.match(r"(?:(?:then|do|else) )?return\b", d.text) for d in cmds[start:i]) for i, c in enumerate(cmds[start:stop], start)))  # cleared before any return
def registered_at(cmds: list, j: int, k: int, var: str, errexit: bool, captured: set = frozenset()) -> bool:  # cmds[j] registers var; a failure stops before any use
    words, nxt = list(cmds[j].words), cmds[j + 1] if j + 1 < len(cmds) else Cmd(0, [], "", 0)
    lead = words.pop(0) if words[:1] in (["if"], ["then"], ["do"]) else ""; negated = words[:1] == ["!"] and bool(words.pop(0))  # noqa: E702
    safe = r"(?:if )?(?:" + CI_TEST + r"|\[\[? -n \"?\$\{?" + var + r"\}?\"? \]\]?)"  # CI, or var non-empty
    if not re.fullmatch(r"(?:pg_register_secret|gha_mask_value|gha_mask_env [A-Z_][A-Z0-9_]*) \"\$\{?" + var + r"\}?\"", " ".join(words)) or (cmds[j].sub or cmds[j].func in captured) and words[0] != "pg_register_secret": return False  # a $(...) captures the mask, also in a function called there; pg_register_secret writes to PG_MASK_FD, a copy of the runner stdout
    if negated:  # if [safe tests &&] ! register "$v"; then ...; exit|return; fi
        head = j
        while lead != "if" and head and cmds[head - 1].op == "&&" and cmds[head].words[:1] != ["if"]:
            head -= 1
            if not re.fullmatch(safe, cmds[head].text): return False
        return cmds[head].words[:1] == ["if"] and cmds[head].stack == cmds[k].stack and cmds[j].op in (";", "\n") and nxt.words[:1] == ["then"] and exits(cmds, j + 1, cmds[head].stack + (head,), var, k)
    bail = nxt.words[:1] in (["exit"], ["return"]) and exits(cmds, j + 1, nxt.stack, var, k) or (not nxt.words and nxt.op == "{" and exits(cmds, j + 2, nxt.stack + (j + 1,), var, k))
    if lead == "if" or (j and cmds[j - 1].op in ("&&", "||", "|", "|&")) or not (cmds[j].op == "||" and bail or cmds[j].op in ("\n", ";", "", ")") and errexit): return False
    level, home = cmds[j].stack, cmds[k].stack; gate = level[-1] if level and level[:-1] == home else -1  # noqa: E702  # one if deeper, on a safe test
    return level == home or gate >= 0 and cmds[gate].words[:1] == ["if"] and bool(re.fullmatch(safe, cmds[gate].text)) and not any(c.stack == level and c.words[:1] in (["else"], ["elif"]) for c in cmds[gate + 1:j])
def state(cmds: list, k: int, var: str, flags: list, captured: set = frozenset()) -> str:  # registered, late (used or traced first) or unregistered; reassignment ends it
    ref, harmless, length, used = re.compile(r"\$\{?" + var + r"(?!\w)|\bexport\s+" + var + r"\b(?!=)"), re.compile(r"-[nz]\s+\"?\$\{?" + var + r"\}?\"?|\$\{#" + var + r"\}"), re.compile(r"\$\{#" + var + r"\}"), False  # -n/-z reads no value only in a test command ([, [[ or test); echo -n prints it
    for j in range(k + 1, len(cmds)):
        if ref.search((harmless if TESTCMD.match(cmds[j].text) else length).sub("", cmds[j].text)):
            if registered_at(cmds, j, k, var, flags[j][0], captured): return "late" if used or any(on[1] for on in flags[k:j + 1]) else "registered"
            used = True
        elif (m := ASSIGN.fullmatch(cmds[j].text)) and m.group(1) == var: break
    return "unregistered"
def shallow(text: str) -> str:  # the text without command substitutions nested below its first level; quoted parens are literal
    out, stack = "", [""]
    for tok in re.findall(r"\\.|\$\(|.", text, re.S):  # an escape, "$(", or one character
        if (top := stack[-1]) == "'" and tok[-1] == "'" or top == '"' == tok or tok == ")" and top in ("$(", "("): stack.pop()
        elif top != "'" and (tok == "$(" or tok in ("(", "'", '"') and top != '"'): stack.append(tok)
        out += tok if stack.count("$(") <= 1 else ""
    return out
def callers(cmds: list, flags: list, prev: tuple, ign: set = frozenset()) -> tuple:  # names of functions called where bash ignores errexit (also in an ign block), called with errexit off, called inside a $(...); or called from such a function
    defs, found = {c.func for c in cmds} - {""}, (set(), set(), set())
    for i, c in enumerate(cmds):  # bash ignores errexit for a tested status, in the whole pipeline: in an && or || list but its last command, in an if, elif, while or until condition, or after `!`
        if (name := (c.words + [""])[(lead := next((n for n, w in enumerate(c.words) if w not in PREFIX_WORDS and not WORD_ASSIGN.fullmatch(w) and not RWORD.match(w)), len(c.words)))]) in defs:  # a redirection word (</dev/null main) is not the command name
            for names, hit in zip(found, (ends(cmds, i) in ("&&", "||") or not {"if", "elif", "while", "until", "!"}.isdisjoint(c.words[:lead]) or cond(cmds, i) or c.func in prev[0] or not ign.isdisjoint(c.stack), not flags[i][0], c.sub or c.func in prev[2])): names.add(name) if hit else None
    return found
def options(cmds: list, errexit: bool, quiet: set = frozenset(), cold: set = frozenset(), ign: set = frozenset(), xtrace: bool = False) -> list:  # [k]: (errexit, xtrace) at cmds[k]; a set applies in text order within its scope: top level, function body or subshell; quiet functions and ign blocks ignore errexit, cold functions start without it
    scope = lambda c: next((s for s in reversed(c.stack) if subshell(cmds, s) or cmds[s].op == "{" and cmds[s].words), -1)
    shells, flat, fns = lambda c: {-1, *(s for s in c.stack if subshell(cmds, s))}, lambda i: runs(cmds, i) and (cmds[i].stack[-1:] or (-1,))[0] == scope(cmds[i]), any(c.op == "{" and c.words for c in cmds)  # the subshells around it; runs in straight line in its scope; the file defines a function
    def wide(i: int, c: Cmd, o: tuple, v: bool) -> bool:  # c sets o to v and may apply out of text order, throughout its innermost subshell: in a loop there, tracing where a function may run, or left by a function
        if c.sub or flag(c, not v, *o) != v: return False
        m = next((h for h in range(i + 1, len(cmds)) if not (d := cmds[h]).sub and scope(d) == scope(c) and (flag(d, True, *o) == flag(d, False, *o) or re.match(r"(?:(?:then|do|else) )?return\b", d.text))), None)
        return any(cmds[s].words[:1] in (["while"], ["until"], ["for"], ["select"]) and s > max(shells(c)) for s in c.stack) or v and fns or scope(c) >= 0 and cmds[scope(c)].op == "{" and not (m is not None and flat(m) and flag(cmds[m], v, *o) != v)  # a function's next change of o, before any return, must reset it in straight line
    off, on, traced, now, out = {max(shells(c)) for i, c in enumerate(cmds) if wide(i, c, ERREXIT, False)}, {max(shells(c)) for i, c in enumerate(cmds) if wide(i, c, XTRACE, True)}, any(c.sub and flag(c, False, *XTRACE) for c in cmds), {-1: (errexit, xtrace)}, []  # traced: a $(...) turns tracing on, assumed in every $(...)
    for i, c in enumerate(cmds):
        e, x = now[s] if (s := scope(c)) in now else now.setdefault(s, ((p := now[scope(cmds[s])])[0] and not (cmds[s].op == "{" and c.func in cold), p[1])); out.append((e and not c.sub and not off & shells(c) and c.func not in quiet and ign.isdisjoint(c.stack), x or bool(on & shells(c)) or bool(c.sub) and traced))  # noqa: E702  # a subshell or function body starts from its parent; bash clears errexit in a $(...)
        if not c.sub: now[s] = (flag(c, e, *ERREXIT), flag(c, x, *XTRACE)) if flat(i) else (e and flag(c, True, *ERREXIT), x or flag(c, False, *XTRACE))  # one that may not run, or runs in another shell, only disables errexit or starts tracing
    return out + [(now[-1][0] and -1 not in off, now[-1][1] or -1 in on)]  # then the top level after the last command
def subshell(cmds: list, s: int) -> bool: return cmds[s].op == "(" and cmds[s].words[:1] != ["case"]  # a ( ) block; case x in (pattern) puts its first pattern's ( on the case line
def left(cmds: list, flags: list) -> tuple:  # (errexit, xtrace) a sourced file may leave in its caller: its end state, merged with the state at each return bash runs in the file's own shell
    ret = [flags[k] for k, c in enumerate(cmds) if re.match(r"(?:(?:if|elif|while|until|then|do|else|!) )*return\b", c.text) and not c.sub and not c.func and not any(subshell(cmds, s) for s in c.stack) and c.op not in ("|", "|&", "&") and next((d.op for d in reversed(cmds[:k]) if not d.sub), "") not in ("|", "|&")]
    return all(e for e, _ in ret + flags[-1:]), any(x for _, x in ret + flags[-1:])
def facts(text: str, line: int = 1, errexit: bool = False, parsed: tuple = (), xtrace: bool = False) -> tuple:  # (cmds, unparsable line, [(k, var, kind, state)], [(k, var, bytes)], options, errexit)
    cmds, error = parsed or parse(text, line)
    ctx, seen, acqs, ign = (set(), set(), set()), None, [], hot(cmds)
    while ctx != seen: seen, flags = ctx, options(cmds, errexit, *ctx[:2], ign, xtrace); ctx = callers(cmds, flags, ctx, ign)  # noqa: E702  # each round only adds names, so it ends
    for k, cmd in enumerate(cmds):
        var, rhs = (m.group(1), m.group(2)) if (m := ASSIGN.fullmatch(cmd.text)) else ("-", "")
        kind = "" if "idem" in var.lower() else "auth-response" if JQ.search(rhs) else "generated" if NAME.search(var) and GEN.search(shallow(rhs)) else ""
        if kind or (not m and not cmd.sub and JQ.search(cmd.text) and not DEVNULL.search(cmd.text)):  # a bare extraction prints it
            acqs.append((k, var, kind or "auth-response", state(cmds, k, var, flags, ctx[2]) if kind else "unregistered"))
        helper = re.fullmatch(r"gha_mask_env(?:_from_command((?: [A-Z][A-Z0-9_]*)+) --| ([A-Z][A-Z0-9_]*)) .+", cmd.text, re.S)  # fail-closed: masks, then exports
        acqs += [(k, name, "masked-env", "unregistered" if cmd.sub or cmd.func in ctx[2] else "late" if flags[k][1] else "registered") for name in (helper.group(1) or helper.group(2)).split()] if helper else []  # in a $(...), the mask is captured but the GITHUB_ENV write still lands
    return cmds, error, acqs, [(k, m.group(1), int(m.group(2)) + int(m.group(3)) if m.group(2) else 10 ** 6) for k, c in enumerate(cmds) for m in PREFIX.finditer(c.text) if m.group(1) in {a[1] for a in acqs} or NAME.search(m.group(1))], flags, errexit
def expand(word: str, table: dict, env: dict, here: str) -> str:  # script-dir and cd/pwd idioms, known variables, defaults; drops quotes
    def lookup(m: re.Match) -> str:
        name, op, default = m.group(1) or m.group(4), m.group(2), m.group(3); value = table[name] if name in table else env[name] if name in env else "." if name in ("GITHUB_WORKSPACE", "PWD") else None  # noqa: E702
        return default if op and (value is None or op[0] == ":" and value == "") else m.group(0) if value is None else value
    for _ in range(12):
        if word == (word := VAR.sub(lookup, TOPLEVEL.sub(".", CDPWD.sub(r"\1", DIRNAME.sub(here, word))))): break
    return word.replace('"', "").replace("'", "")
def shell_operand(args: list) -> tuple:  # bash/sh: (script, argv, False, traced, rc) or (-c program, [], True, traced, rc); "" is stdin; -c takes the first operand after all options, over -s; rc: the --rcfile/--init-file operand, kept only with -i
    i, traced, program, stdin, rc, interactive = 0, False, False, False, "", False
    while i < len(args) and args[i][:1] in ("-", "+") and args[i] != "--":
        short, rc, traced = not args[i].startswith("--"), (args[i + 1:i + 2] or [""])[0] if args[i] in ("--rcfile", "--init-file") else rc, traced or bool(re.fullmatch(r"-[a-zA-Z]*x[a-zA-Z]*", args[i]) or re.fullmatch(r"-[a-zA-Z]*o", args[i]) and args[i + 1:i + 2] == ["xtrace"])
        program, stdin, interactive, i = program or short and "c" in args[i], stdin or short and "s" in args[i], interactive or short and "i" in args[i], i + (2 if short and args[i][-1] in "oO" or args[i] in ("--rcfile", "--init-file") else 1)  # -o and -O take an option name, --rcfile and --init-file a file; bash 5.2 and 5.3 refuse the --rcfile=F form
    i, rc = i + (args[i:i + 1] == ["--"]), rc if interactive else ""  # -i: bash runs the rcfile first; --norc, a login shell, --posix, +i and sh skip it, and scanning it there too fails closed
    return (args[i], [], True, traced, rc) if program and i < len(args) else ("", [], False, traced, rc) if program or stdin or i >= len(args) else (args[i], args[i + 1:], False, traced, rc)
def literal_env(node: object) -> dict: return {str(k): "" if v is None else str(v) for k, v in node.items()} if isinstance(node, dict) else {}
def github_env_names(run: str) -> set: return set(re.findall(r"(?m)^\s*gha_mask_env\s+([A-Za-z_]\w*)", run)) | set(re.findall(r"(?m)^\s*(?:echo|printf)\s+(?:'[^']*'\s+)?[\"']?([A-Za-z_]\w*)=.*>>\s*[\"']?\$\{?GITHUB_ENV\b", run)) | {n for args in re.findall(r"(?m)^\s*gha_mask_env_from_command\s+(.+?)\s+--\s", run) for n in re.findall(r"[A-Za-z_]\w*", args)}  # names a run step writes to GITHUB_ENV (masked or plain)
def run_lines(text: str) -> dict:  # (job id, step index) -> file line just before the step's run script, or of its uses
    def get(node: object, key: str) -> object: return next((v for k, v in node.value if getattr(k, "value", None) == key), None) if isinstance(node, yaml.MappingNode) else None
    found, jobs = {}, get(yaml.compose(text, Loader=yaml.SafeLoader), "jobs")
    for job_key, job in (jobs.value if isinstance(jobs, yaml.MappingNode) else []):
        steps = get(job, "steps")
        for index, run in enumerate(get(step, "run") or get(step, "uses") for step in (steps.value if isinstance(steps, yaml.SequenceNode) else [])):
            if run is not None: found[(job_key.value, index)] = run.start_mark.line + (run.style in ("|", ">"))
    return found
def guarded(cmds: list, k: int, guard: str) -> bool:  # the source or a block around it, or every (>= 1) call site of its function
    def under(cmd: Cmd, own: tuple = ()) -> bool: return any(re.fullmatch(guard, cmds[s].text) for s in cmd.stack + own)
    return under(cmds[k], (k,)) or (bool(calls := [c for c in cmds if cmds[k].func and cmds[k].func in c.words]) and all(under(c) for c in calls))
NATIVE = (b"\x7fELF", b"MZ", b"\xfe\xed\xfa\xce", b"\xfe\xed\xfa\xcf", b"\xce\xfa\xed\xfe", b"\xcf\xfa\xed\xfe", b"\xca\xfe\xba\xbe")  # ELF, PE, Mach-O (32- and 64-bit, both byte orders), fat Mach-O
def native(path: pathlib.Path) -> bool:  # a native executable whose first line holds a NUL, which bash's own binary test refuses: the kernel runs it, bash never reads it as a script
    try:
        with path.open("rb") as handle: head = handle.read(80)  # bash tests at most the first 80 bytes
    except OSError: return False  # unreadable: it stays oversized
    return head.startswith(NATIVE) and b"\x00" in head.split(b"\n", 1)[0]
class Scan:  # walks each job's run steps through their transitive shell and make edges
    def __init__(self, root: pathlib.Path) -> None: self.root, self.cache, self.issues, self.inventory, self.exec_skip, self.bang_x = root, {}, set(), set(), set(), set()
    def report(self, code: str, rel: str, line: int, source: bool = False) -> None: self.issues.add((self.wf, self.job, f"{code}@{rel}:{line}")) if self.credential or not source else None  # SOURCE_*: credential jobs only
    def read(self, rel: str) -> tuple:  # (text, refusal): a regular in-repo file, no symlinked component, <= MAX_BYTES
        path = self.root
        if any((path := path / part).is_symlink() for part in rel.split("/")): return "", "SYMLINK"
        refusal = "MISSING" if not path.is_file() else "ESCAPE" if not path.resolve().is_relative_to(self.root.resolve()) else "OVERSIZE" if path.stat().st_size > MAX_BYTES else ""
        return ("", refusal) if refusal else (path.read_bytes().decode("utf-8", "replace").replace("\r\n", "\n"), "")
    def rules(self) -> object:  # ({target: (line, prerequisites, [(line, recipe)])}, default goal), or a refusal
        if "Makefile" not in self.cache:
            (text, code), rules, default, current = self.read("Makefile"), {}, "", []
            for n, line in enumerate(text.split("\n"), 1):
                if line.startswith("\t") and current:
                    for target in current: rules[target][2].append((n, line[1:].lstrip("@-+ \t")))
                elif (m := re.match(r"([^\s:=#][^:=#]*?)\s*::?(?!=)\s*([^=]*)$", line)) and not line.startswith(("export ", "override ", "define ")):
                    current = m.group(1).split(); default = default or next((t for t in current if not t.startswith(".")), "")  # noqa: E702
                    for target in current: rules.setdefault(target, (n, [], []))[1].extend(m.group(2).split(";")[0].split())
                elif line.strip() and not line.lstrip().startswith("#"): current = []
            self.cache["Makefile"] = code or (rules, default)
        return self.cache["Makefile"]
    def rule(self, target: str) -> object: return None if isinstance(rules := self.rules(), str) else rules[0].get(target) or next((r for p, r in rules[0].items() if "%" in p and re.fullmatch(re.escape(p).replace("%", ".+"), target)), None)
    def load(self, key: str) -> object:  # facts for a script; for a make target, a list: its prerequisite rules as a make line, then each shell its recipe runs in
        if key not in self.cache and not key.startswith("Makefile#"):
            text, code = self.read(key); self.cache[key] = code or ("NOT_SHELL" if "\x00" in text else facts(text))  # noqa: E702  # source and bash read any text as shell
            if not (code or key.endswith(".sh") or re.match(r"#!.*\b(?:ba)?sh\b", text) or not text.startswith("#!") and self.root.joinpath(key).stat().st_mode & 0o100) or code == "OVERSIZE" and native(self.root.joinpath(key)): self.exec_skip.add(key)  # exec: sh #! or ENOEXEC; an oversized native executable runs natively: an exec edge skips it, source and bash still report it
            if re.match(r"#![^\n]*?(?<!\S)-[a-zA-Z]*x", text): self.bang_x.add(key)  # a #! -x flag traces the file, assumed on every edge
        elif key not in self.cache:
            rules, found = self.rules(), self.rule(key[9:])
            self.cache[key] = rules if isinstance(rules, str) else "MISSING"
            if found is not None:
                deps, parts = [p for p in found[1] if "$" in p or self.rule(p) is not None], []  # parts: (first line, text) per shell
                for n, text in [(found[0], "make " + " ".join(deps) if deps else "")] + [(n, t.replace("$(MAKE)", "make").replace("${MAKE}", "make").replace("$(CURDIR)", ".").replace("$$", "$")) for n, t in found[2]]:
                    parts[-1:] = [(parts[-1][0], parts[-1][1] + "\n" * (n - parts[-1][0] - parts[-1][1].count("\n")) + text)] if parts and (parts[-1][1].endswith("\\") or len(parts) > 1 and ".ONESHELL" in rules[0]) else parts[-1:] + [(n, text)]
                self.cache[key] = [facts(text, n) for n, text in parts]  # make runs each logical recipe line (backslash-continued) in its own sh -c, errexit off; .ONESHELL runs the recipe in one
        return self.cache[key]
    def follow(self, key: str, env: dict, argv: list, chain: tuple, quiet: bool, caller: str, line: int, binary_ok: bool = False) -> None:
        unit = "CYCLE" if key in chain else "LIMIT" if len(chain) >= MAX_DEPTH or self.visits >= MAX_VISITS else self.load(key)
        unit, env = "NOT_SHELL" if binary_ok and key in self.exec_skip else unit, dict(env, SHELLOPTS="xtrace") if key in self.bang_x else env  # an exec'd foreign or data file
        if isinstance(unit, str): self.report("SOURCE_" + unit, caller, line, True) if unit != "NOT_SHELL" and not (binary_ok and unit == "MISSING") else None
        elif (memo := (key, tuple(sorted(env.items())), tuple(argv), quiet)) not in self.seen:
            self.seen.add(memo); self.visits += 1  # noqa: E702
            for part in unit if isinstance(unit, list) else [unit]: self.visit(key.split("#")[0], part, env, argv, chain + (key,), quiet, ".")  # each shell of a make recipe starts from make's environment
    def visit(self, rel: str, unit: tuple, env: dict, argv: list, chain: tuple, quiet: bool, cwd: str) -> None:
        cmds, error, acqs, prefixes, flags, _ = unit = self.settle(rel, unit, env, argv, chain, quiet, cwd, unit[5])
        if error: self.report("SOURCE_DYNAMIC", rel, error, True)
        for k, var, kind, status in acqs:
            status = "late" if status == "registered" and "xtrace" in env.get("SHELLOPTS", "") else "exempt" if status != "registered" and (exempt := EXEMPT.get((rel, var))) and exempt[1](env) and guarded(cmds, k, exempt[0]) else status
            self.inventory.add(f"{self.wf}:{self.job}:{kind}@{rel}:{cmds[k].line}:{var}:{status}")
            if status in ("unregistered", "late"): self.report("CREDENTIAL_UNREGISTERED" if status == "unregistered" else "CREDENTIAL_USE_BEFORE_REGISTRATION", rel, cmds[k].line)
        for k, var, need in prefixes:
            if not ((allowed := PREFIX_EXEMPT.get((rel, var))) and allowed(env, quiet and ">&2" not in cmds[k].text, need)): self.report("CREDENTIAL_PREFIX_PRINT", rel, cmds[k].line)
        self.walk(rel, unit, env, argv, chain, quiet, cwd, False)
    def walk(self, rel: str, unit: tuple, env: dict, argv: list, chain: tuple, quiet: bool, cwd: str, dry: bool) -> dict:  # follows every edge; dry: only {k: what the file sourced at cmds[k] leaves set}
        table, env, traced, sets = {}, dict(env), "xtrace" in env.get("SHELLOPTS", ""), {}
        for k, (cmd, (_, on)) in enumerate(zip(unit[0], unit[4])):  # xtrace in force at an edge reaches its child, as if SHELLOPTS were exported
            if not traced and on != ("xtrace" in env.get("SHELLOPTS", "")): env["SHELLOPTS"] = "xtrace" * on
            self.track(cmd.words, table, env, posixpath.dirname(rel) or "."); sets[k] = self.edge(cmd, rel, table, env, argv, chain, quiet, cwd, dry) if not dry or {"source", "."} & {w.strip("\"'") for w in cmd.words} or "$" in cmd.text else ""  # noqa: E702  # dry: only a source or . word, or an expansion, can name a source
        return {k: s for k, s in sets.items() if s}
    def settle(self, rel: str, unit: tuple, env: dict, argv: list, chain: tuple, quiet: bool, cwd: str, errexit: bool) -> tuple:  # the unit with each sourced file's leftover options applied at its source command, as a set there is
        sets = self.walk(rel, unit, env, argv, chain, quiet, cwd, True)
        return facts("", 0, errexit, ([c._replace(sets=sets.get(k, "")) for k, c in enumerate(unit[0])], unit[1])) if sets or errexit != unit[5] else unit
    def effect(self, key: str, env: dict, argv: list, chain: tuple, quiet: bool, rel: str, line: int) -> str:  # what a file run in its caller's shell leaves set there, at its end or a return: e (errexit may be off) or E (on at every exit), x (tracing may be on) or X (off at every exit), or ex if unparsable or past the job's dry budget; a refused, cyclic or too-deep file leaves nothing (its edge reports it)
        if posixpath.isabs(key) or key == ".." or key.startswith("../") or not isinstance(unit := "CYCLE" if key in chain else "LIMIT" if len(chain) >= MAX_DEPTH else self.load(key), tuple): return ""  # an absolute or escaping file is refused unread, as edge() refuses a sourced one; the rcfile call reaches here without that check
        if (request := (key, tuple(sorted(env.items())), tuple(argv), chain, quiet)) in self.dry: return self.dry[request]  # this job's analyses; chain keeps cycle and depth context
        if len(self.dry) >= MAX_DRY: return self.dry.get(None) or self.report("SOURCE_LIMIT", rel, line, True) or self.dry.setdefault(None, "ex")  # the job's dry budget, apart from MAX_VISITS, is spent: report once, then fail closed with both options
        if (memo := ("effect", key, tuple(sorted(self.walk(key, unit, env, argv, chain + (key,), quiet, ".", True).items())))) not in self.cache: parsed = ([c._replace(sets=dict(memo[2]).get(k, "")) for k, c in enumerate(unit[0])], unit[1]); (e1, x1), (e0, x0) = (left(parsed[0], facts("", 0, s, parsed, not s)[4]) for s in (True, False)); self.cache[memo] = "ex" if unit[1] else ("e" if not e1 else "E" if e0 else "") + ("x" if x1 else "X" if not x0 else "")  # noqa: E702
        self.dry[request] = self.cache[memo]; return self.cache[memo]  # noqa: E702
    def track(self, words: list, table: dict, env: dict, here: str) -> None:  # a pure assignment, export, declaration or unset
        keyword = words[0] if words[:1] and words[0] in ("export", "local", "declare", "typeset", "readonly", "unset") else ""
        items = [w for w in words[bool(keyword):] if not w.startswith("-")]
        if not items or not all(WORD_ASSIGN.fullmatch(w) or (keyword and re.fullmatch(r"[A-Za-z_]\w*", w)) for w in items): return
        for item in items:
            name = (m := WORD_ASSIGN.fullmatch(item)) and m.group(1) or item
            if keyword == "unset": table.pop(name, None); env.pop(name, None); continue  # noqa: E702
            if m: table[name] = expand(m.group(2), table, env, here)
            if keyword == "export" or (keyword == "declare" and "-x" in words) or (m and name in env and keyword != "local"): env[name] = table.get(name, env.get(name, ""))
    def edge(self, cmd: Cmd, rel: str, table: dict, env: dict, argv: list, chain: tuple, quiet: bool, cwd: str, dry: bool = False) -> object:
        here, words, child = posixpath.dirname(rel) or ".", list(cmd.words), dict(env)
        quiet, words = quiet or any(re.fullmatch(r"(?:1|&)?>>?/dev/null", w) for w in words), [w for w in words if not RWORD.match(w)]  # a redirection word is neither a prefix nor the command name (</dev/null env FOO=1 bash x.sh)
        while words and (words[0] in PREFIX_WORDS or WORD_ASSIGN.fullmatch(words[0])):
            if m := WORD_ASSIGN.fullmatch(word := words.pop(0)): child[m.group(1)] = expand(m.group(2), table, env, here)
            while word in ("env", "sudo", "timeout") and words and (words[0][:1] == "-" or (word == "timeout" and re.fullmatch(r"[\d.]+[smhd]?", words[0]))):
                if words.pop(0) in ("-u", "--unset") and words: child.pop(words.pop(0), None)
        name, program, dynamic = expand(words[0], table, env, here) if words else "", False, lambda: None if dry else self.report("SOURCE_DYNAMIC", rel, cmd.line, True)
        if dry and name not in ("source", "."): return ""
        if name == "eval": return dynamic()
        if name in ("make", "gmake"): return self.make([expand(w, table, env, here) for w in words[1:]], rel, cmd.line, child, chain, quiet)
        if name in ("source", "."): target, rest, kind = (words + [""])[1], words[2:], "source"  # no literal operand (<(...)) is dynamic
        elif posixpath.basename(name) in ("bash", "sh"): (target, rest, program, traced, rc), kind = shell_operand(words[1:]), "shell"; child.update({"SHELLOPTS": "xtrace"} if traced else {}); (dynamic() if "$" in (rcpath := expand(rc, table, env, here)) else (self.edge(cmd._replace(words=[name, rcpath]), rel, table, child, argv, chain, quiet, cwd), child.update({"SHELLOPTS": "xtrace"} if "x" in self.effect(posixpath.normpath(posixpath.join(cwd, rcpath)), child, [], chain, quiet, rel, cmd.line) else {}))) if rc else None  # noqa: E702  # an interactive shell runs its rcfile first, in that same shell, so tracing the rcfile leaves on traces the program too; the caller expands its name, as for the script, so one it cannot resolve is dynamic
        elif "/" in name: target, rest, kind = words[0], words[1:], "exec"
        else: return dynamic() if any(w.strip("\"'").endswith(".sh") for w in words[1:]) and ("$" in name or any(posixpath.basename(w.strip("\"'")) in ("bash", "sh") for w in words[1:])) else None  # a wrapper or variable command runs a script
        if program:  # a literal -c program is scanned inline; one whose command word (after assignments, redirections and prefix words) expands is dynamic
            unit = facts(target[1:-1] if len(target) > 1 and target[0] == target[-1] and target[0] in "'\"" else target, cmd.line)
            return dynamic() if any(re.search(r"[$`]", next((w for w in c.words if w not in PREFIX_WORDS and not WORD_ASSIGN.fullmatch(w) and not RWORD.match(w)), "")) for c in unit[0]) else self.visit(rel, unit, child, [], chain, quiet, cwd)
        path = expand(target, table, env, here)
        if not target or re.search(r"[$`\x00]", path): return dynamic() if kind != "exec" or path.endswith(".sh") else None
        key = posixpath.normpath(posixpath.join(cwd, path))
        if kind == "exec" and posixpath.isabs(path) and not path.endswith(".sh"): return None
        if posixpath.isabs(key) or key == ".." or key.startswith("../"): return None if dry else self.report("SOURCE_ESCAPE", rel, cmd.line, True)
        if GATES.get((rel, cmd.func, key), lambda _: True)(argv): return self.effect(key, child, [expand(w, table, env, here) for w in rest], chain, quiet, rel, cmd.line) if dry else self.follow(key, child, [expand(w, table, env, here) for w in rest], chain, quiet, rel, cmd.line, kind == "exec" and not key.endswith(".sh"))
        return None
    def make(self, args: list, rel: str, line: int, env: dict, chain: tuple, quiet: bool) -> None:
        targets, skip, rules = [], False, self.rules()
        for arg in args:
            if re.match(r"-[Cf]|--(?:directory|file|makefile)\b", arg) or ("$" in arg and "=" not in arg): return self.report("SOURCE_DYNAMIC", rel, line, True)
            targets, skip = targets + ([] if skip or arg[:1] == "-" or "=" in arg else [arg]), arg in ("-j", "-l", "-o", "-W", "-I")
        if isinstance(rules, str): return self.report("SOURCE_" + rules, rel, line, True)
        for target in targets or [rules[1]]: self.follow("Makefile#" + target, env, [], chain, quiet, rel, line)
        return None
    def run_job(self, wf: str, job_id: str, job: dict, workflow: dict, lines: dict, credential: bool) -> None:
        self.wf, self.job, self.credential, self.seen, self.visits, self.dry, exported = wf, job_id, credential, set(), 0, {}, set()
        rel, base, defaults = ".github/workflows/" + wf, {**literal_env(workflow.get("env")), **literal_env(job.get("env"))}, [(node.get("defaults") or {}).get("run") or {} for node in (job, workflow)]
        for index, step in enumerate(job.get("steps") or []):
            if isinstance(step, dict) and "run" not in step and str(step.get("uses", "")).startswith("./"): self.report("SOURCE_DYNAMIC", rel, lines.get((job_id, index), 0) + 1, True)  # unscanned local action
            if not isinstance(step, dict) or "run" not in step: continue
            shell, cwd = (str(step.get(key) or defaults[0].get(key) or defaults[1].get(key) or "") for key in ("shell", "working-directory"))
            if shell and not re.match(r"(?:\S*/)?(?:env\s+)?(?:ba)?sh(?=\s|$)", shell): self.report("SOURCE_DYNAMIC", rel, lines.get((job_id, index), 0) + 1, True); continue  # unscanned shell
            env, errexit = {**base, **dict.fromkeys(exported, UNKNOWN), **literal_env(step.get("env")), **({"SHELLOPTS": "xtrace"} if re.search(r"(?<!\S)-[a-zA-Z]*x|-o\s+xtrace", shell) else {})}, shell in ("", "bash", "sh") or bool(re.search(r"\s-\w*e", shell))
            self.visit(rel, facts(str(step["run"]), lines.get((job_id, index), 0) + 1, errexit), env, [], (rel,), False, posixpath.normpath(cwd or "."))
            exported |= github_env_names(str(step["run"]))
def transitive(root: pathlib.Path, credential_jobs: set) -> tuple:  # issues (workflow, job, CODE@path:line) and value-free inventory
    scan = Scan(root)
    for path in sorted((root / ".github/workflows").glob("*.y*ml")):
        try: workflow, lines = yaml.safe_load(text := path.read_text(encoding="utf-8")), run_lines(text)
        except Exception: continue  # workflow_credential_guard.py reports YAML_PARSE
        jobs = workflow.get("jobs") if isinstance(workflow, dict) else None
        for job_id, job in (jobs.items() if isinstance(jobs, dict) else []): scan.run_job(path.name, str(job_id), job if isinstance(job, dict) else {}, workflow, lines, (path.name, job_id) in credential_jobs)
    return sorted(scan.issues), sorted(scan.inventory)
def main(argv: list) -> int: return print("\n".join(transitive(pathlib.Path(argv[1]), set())[1])) or 0 if len(argv) == 2 and argv[0] == "--inventory" else print("usage: workflow_credential_sources.py --inventory ROOT", file=sys.stderr) or 2
if __name__ == "__main__": raise SystemExit(main(sys.argv[1:]))
