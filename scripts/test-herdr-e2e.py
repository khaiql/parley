#!/usr/bin/env python3
"""Exercise real Herdr, Parley processes and isolated OpenSSH transport."""

import argparse
import sys
sys.dont_write_bytecode = True
from herdr_e2e.environment import Lab
from herdr_e2e.scenarios import run_scenarios


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", help="New short private directory for retained evidence")
    parser.add_argument("--scenario", default="all", help="Scenario name, or all")
    args = parser.parse_args()
    lab = Lab(args.output)
    print(f"Evidence: {lab.root}", flush=True)
    try:
        run_scenarios(lab, args.scenario)
    finally:
        lab.close()


if __name__ == "__main__":
    main()
