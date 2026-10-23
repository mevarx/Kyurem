import argparse

from kyurem_demo.agents import serve

parser = argparse.ArgumentParser(description="Run a Kyurem Python A2A demo agent")
parser.add_argument("agent", choices=("echo", "upper", "flaky"))
args, rest = parser.parse_known_args()
serve(args.agent, rest)
