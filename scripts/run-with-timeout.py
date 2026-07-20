#!/usr/bin/env python3
"""Run a command in its own process group with a portable hard timeout."""

from __future__ import annotations

import argparse
import os
import signal
import subprocess
import sys


def main() -> int:
    parser = argparse.ArgumentParser(
        description="run a command with a timeout and terminate its entire process group"
    )
    parser.add_argument("seconds", type=int)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if args.seconds <= 0 or not command:
        parser.error("provide a positive timeout and a command after --")

    process = subprocess.Popen(command, start_new_session=True)
    try:
        return process.wait(timeout=args.seconds)
    except subprocess.TimeoutExpired:
        print(
            f"run-with-timeout: command exceeded {args.seconds}s: {' '.join(command)}",
            file=sys.stderr,
        )
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
        return 124
    except KeyboardInterrupt:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
        return 130


if __name__ == "__main__":
    sys.exit(main())
