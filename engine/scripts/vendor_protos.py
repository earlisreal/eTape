#!/usr/bin/env python3
"""Vendor moomoo OpenD .proto files into the engine repo with go_package rewritten
to eTape's module path. Run once, and again whenever the moomoo SDK is upgraded.
Output (internal/feed/opend/pb/proto/*.proto) is committed."""
import os, re, shutil, sys
import moomoo

SDK_PB = os.path.join(os.path.dirname(moomoo.__file__), "common", "pb")
ENGINE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))  # engine/
DEST = os.path.join(ENGINE, "internal", "feed", "opend", "pb", "proto")
MODULE_PB = "github.com/earlisreal/eTape/engine/internal/feed/opend/pb"

def go_dir(pkg: str) -> str:
    # futu convention: lowercase, drop underscores (Qot_Common -> qotcommon)
    return pkg.lower().replace("_", "")

def main() -> None:
    if os.path.isdir(DEST):
        shutil.rmtree(DEST)
    os.makedirs(DEST)
    count = 0
    for name in sorted(os.listdir(SDK_PB)):
        if not name.endswith(".proto"):
            continue
        text = open(os.path.join(SDK_PB, name), encoding="utf-8").read()
        m = re.search(r"^package\s+([A-Za-z0-9_]+)\s*;", text, re.M)
        if not m:
            sys.exit(f"no package declaration in {name}")
        gp = f'option go_package = "{MODULE_PB}/{go_dir(m.group(1))}";'
        if re.search(r"^option\s+go_package", text, re.M):
            text = re.sub(r"^option\s+go_package\s*=.*$", gp, text, count=1, flags=re.M)
        else:
            text = re.sub(r"^(package\s+[A-Za-z0-9_]+\s*;.*)$", r"\1\n" + gp, text, count=1, flags=re.M)
        open(os.path.join(DEST, name), "w", encoding="utf-8").write(text)
        count += 1
    print(f"vendored {count} protos into {DEST}")

if __name__ == "__main__":
    main()
