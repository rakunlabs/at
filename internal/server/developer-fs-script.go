package server

// developerFSScript runs inside the space container and performs one file
// operation under /workspace. It is the single place that resolves paths, so
// every caller (browser file API and the agent's tools) shares the same
// containment rules: symlinks are resolved and must stay inside the root, and
// the result is re-checked after resolution rather than trusting the string.
//
// argv: op root [args...]; bulk payloads (file content) arrive on stdin. The
// script prints one JSON document on success and exits 3 with {"error": ...}
// for a refusal the caller should surface verbatim.
const developerFSScript = `
import base64, hashlib, json, os, pathlib, re, shutil, stat, sys

MAX_READ = 2 * 1024 * 1024
MAX_LIST = 5000

def fail(message):
    sys.stdout.write(json.dumps({"error": message}))
    sys.exit(3)

op, root_arg = sys.argv[1], sys.argv[2]
args = sys.argv[3:]
workspace = pathlib.Path("/workspace").resolve()
root = (workspace / root_arg).resolve() if root_arg else workspace
try:
    root.relative_to(workspace)
except ValueError:
    fail("path escapes the workspace")

def resolve(rel, must_exist=True, allow_root=False):
    rel = rel.strip().strip("/")
    if rel in ("", "."):
        if allow_root:
            return root
        fail("path is required")
    p = (root / rel)
    try:
        resolved = p.resolve(strict=must_exist)
    except FileNotFoundError:
        fail("not found: " + rel)
    try:
        resolved.relative_to(root)
    except ValueError:
        fail("path escapes the project: " + rel)
    return resolved

def relpath(p):
    r = str(p.relative_to(root))
    return "" if r == "." else r

def entry(p):
    st = p.lstat()
    kind = "dir" if stat.S_ISDIR(st.st_mode) else "symlink" if stat.S_ISLNK(st.st_mode) else "file"
    return {"name": p.name, "path": relpath(p), "type": kind, "size": st.st_size, "mtime": int(st.st_mtime)}

def version_of(data):
    return hashlib.sha256(data).hexdigest()[:24]

if op == "list":
    target = resolve(args[0] if args else "", allow_root=True)
    if not target.is_dir():
        fail("not a directory")
    items = []
    for child in sorted(target.iterdir(), key=lambda c: (not c.is_dir() or c.is_symlink(), c.name.lower())):
        try:
            items.append(entry(child))
        except OSError:
            continue
        if len(items) >= MAX_LIST:
            break
    print(json.dumps({"path": relpath(target), "entries": items, "truncated": len(items) >= MAX_LIST}))

elif op == "read":
    target = resolve(args[0])
    if not target.is_file():
        fail("not a file")
    size = target.stat().st_size
    if size > MAX_READ:
        print(json.dumps({"path": relpath(target), "size": size, "too_large": True}))
        sys.exit(0)
    data = target.read_bytes()
    try:
        text = data.decode("utf-8")
        print(json.dumps({"path": relpath(target), "size": size, "content": text, "version": version_of(data)}))
    except UnicodeDecodeError:
        print(json.dumps({"path": relpath(target), "size": size, "binary": True, "version": version_of(data)}))

elif op == "write":
    expected = args[1] if len(args) > 1 else ""
    target = resolve(args[0], must_exist=False)
    data = sys.stdin.buffer.read()
    if target.exists():
        if target.is_dir():
            fail("a directory exists at this path")
        if expected and version_of(target.read_bytes()) != expected:
            fail("conflict: the file changed since it was opened")
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_name("." + target.name + ".at-tmp")
    tmp.write_bytes(data)
    os.replace(tmp, target)
    print(json.dumps({"path": relpath(target), "size": len(data), "version": version_of(data)}))

elif op == "create":
    kind, rel = args[0], args[1]
    target = resolve(rel, must_exist=False)
    if target.exists() or target.is_symlink():
        fail("already exists: " + rel)
    if kind == "dir":
        target.mkdir(parents=True)
    else:
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(b"")
    print(json.dumps(entry(target)))

elif op == "rename":
    source = resolve(args[0])
    if source == root:
        fail("cannot rename the project root")
    dest = resolve(args[1], must_exist=False)
    if dest.exists():
        fail("destination already exists")
    dest.parent.mkdir(parents=True, exist_ok=True)
    os.rename(source, dest)
    print(json.dumps(entry(dest)))

elif op == "delete":
    rel = args[0].strip().strip("/")
    if rel in ("", "."):
        fail("cannot delete the project root")
    p = root / rel
    # lstat, not resolve: deleting a symlink removes the link, never its target.
    try:
        p.parent.resolve(strict=True).relative_to(root)
    except (FileNotFoundError, ValueError):
        fail("not found: " + rel)
    if p.is_symlink() or p.is_file():
        p.unlink()
    elif p.is_dir():
        shutil.rmtree(p)
    else:
        fail("not found: " + rel)
    print(json.dumps({"deleted": rel}))

elif op == "walk":
    target = resolve(args[0] if args else "", allow_root=True)
    limit = 10000
    out = []
    for base, dirs, files in os.walk(target, followlinks=False):
        dirs[:] = sorted(d for d in dirs if d not in (".git", "node_modules") and not (pathlib.Path(base) / d).is_symlink())
        for name in sorted(files):
            p = pathlib.Path(base) / name
            if p.is_symlink():
                continue
            out.append(relpath(p))
            if len(out) >= limit:
                break
        if len(out) >= limit:
            break
    print(json.dumps({"files": out, "truncated": len(out) >= limit}))

elif op == "search":
    pattern, start_rel = args[0], (args[1] if len(args) > 1 else "")
    try:
        rx = re.compile(pattern)
    except re.error as e:
        fail("invalid pattern: " + str(e))
    target = resolve(start_rel, allow_root=True)
    def candidates():
        if target.is_file():
            yield target
            return
        for base, dirs, files in os.walk(target, followlinks=False):
            dirs[:] = sorted(d for d in dirs if d not in (".git", "node_modules"))
            for name in sorted(files):
                yield pathlib.Path(base) / name
    paths = candidates()
    hits = []
    for p in paths:
        if p.is_symlink() or not p.is_file():
            continue
        try:
            if p.stat().st_size > 4 * 1024 * 1024:
                continue
            for no, line in enumerate(p.read_text(encoding="utf-8").splitlines(), 1):
                if rx.search(line):
                    hits.append({"path": relpath(p), "line": no, "text": line[:400]})
                    if len(hits) >= 2000:
                        break
        except (UnicodeDecodeError, OSError):
            continue
        if len(hits) >= 2000:
            break
    print(json.dumps({"matches": hits, "truncated": len(hits) >= 2000}))

else:
    fail("unknown operation")
`
